package cacheapi_test

// Covers redis-cache-mock-api and inmemory-cache-mock-api against their
// live Render URLs (overridable via env vars for local testing).
//
// Both environments share the SAME underlying Postgres row for the test
// user (they're separate cache layers in front of one database), so
// these run sequentially, not in parallel — running them concurrently
// could let one environment's update race against the other's read.
//
// Three cases, run in order per environment:
//   1. GET (first time / not cached)      -> MISS. Ends by forcing a
//      real update, so the cache is guaranteed empty for the next run
//      too (rather than relying on TTL having expired by then).
//   2. GET (second time, within TTL)      -> HIT. Same cleanup at the end.
//   3. PUT after the cache is confirmed set -> deletes the cache entry;
//      the next GET must MISS and reflect the new value.
//
// forceCacheInvalidation() is the answer to "if PUT doesn't actually
// update anything, keep changing the value until it does": the update
// endpoint returns {"message":"No update"} when the new salary equals
// the current DB value, so this helper keeps bumping the candidate
// salary and retrying until it gets back "Success".
//
// Run with:
//   go test ./... -v
// Env vars (all optional, default to the live Render deployments):
//   REDIS_BASE_URL, INMEMORY_BASE_URL, TEST_USER_ID

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userResponse struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Location string `json:"location"`
	Salary   int    `json:"salary"`
}

type updateResponse struct {
	Message string `json:"message"`
}

type environment struct {
	Name    string
	BaseURL string
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func testEnvironments() []environment {
	return []environment{
		{"redis", getenvDefault("REDIS_BASE_URL", "https://redis-cache-mock-api.onrender.com")},
		{"inmemory", getenvDefault("INMEMORY_BASE_URL", "https://inmemory-cache-mock-api.onrender.com")},
	}
}

func testUserID() int {
	if v := os.Getenv("TEST_USER_ID"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			return id
		}
	}
	return 2
}

// ---------- HTTP helpers ----------

func getUser(t *testing.T, baseURL string, userID int) (status int, cacheHeader string, user userResponse) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/api/user?user_id=%d", baseURL, userID))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = json.Unmarshal(body, &user) // best-effort; error responses won't unmarshal, and that's fine

	return resp.StatusCode, resp.Header.Get("X-Cache"), user
}

func putSalary(t *testing.T, baseURL string, userID, salary int) (status int, result updateResponse) {
	t.Helper()
	payload, err := json.Marshal(map[string]int{"user_id": userID, "salary": salary})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/user/update", baseURL), bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	_ = json.Unmarshal(body, &result)

	return resp.StatusCode, result
}

func currentSalary(t *testing.T, baseURL string, userID int) int {
	t.Helper()
	_, _, user := getUser(t, baseURL, userID)
	return user.Salary
}

// forceCacheInvalidation PUTs a genuinely different salary and returns the
// value it succeeded with. If a candidate happens to match the current DB
// value (so the API replies "No update" instead of "Success"), it keeps
// incrementing and retrying rather than accepting a no-op as done.
func forceCacheInvalidation(t *testing.T, baseURL string, userID int) int {
	t.Helper()
	candidate := currentSalary(t, baseURL, userID)

	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		candidate += 1000 + attempt
		status, result := putSalary(t, baseURL, userID, candidate)
		require.Equal(t, http.StatusOK, status, "unexpected status on update attempt %d: %+v", attempt, result)

		switch result.Message {
		case "Success":
			return candidate
		case "No Success":
			t.Fatalf("update failed unexpectedly on attempt %d: %+v", attempt, result)
		case "No update":
			continue // candidate coincided with current value; try a bigger jump
		default:
			t.Fatalf("unexpected update message %q on attempt %d", result.Message, attempt)
		}
	}
	t.Fatalf("could not force a real update after %d attempts", maxAttempts)
	return candidate
}

// ---------- test cases ----------

func testGetIsCacheMissWhenNotCached(t *testing.T, baseURL string) {
	// Guarantee the cache is empty before asserting MISS — "first time or
	// does not exist" only holds if nothing is already sitting in cache
	// from an earlier run.
	forceCacheInvalidation(t, baseURL, testUserID())

	status, cache, user := getUser(t, baseURL, testUserID())
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "MISS", cache)
	assert.Equal(t, testUserID(), user.UserID)

	// End by invalidating again, so the cache is guaranteed empty for
	// whatever runs next — not dependent on the TTL having expired.
	forceCacheInvalidation(t, baseURL, testUserID())
}

func testGetIsCacheHitOnSecondCall(t *testing.T, baseURL string) {
	// First GET populates the cache; not the focus of this test.
	status1, cache1, _ := getUser(t, baseURL, testUserID())
	require.Equal(t, http.StatusOK, status1)
	require.Equal(t, "MISS", cache1)

	// Second GET, within TTL, should be served from cache.
	status2, cache2, _ := getUser(t, baseURL, testUserID())
	assert.Equal(t, http.StatusOK, status2)
	assert.Equal(t, "HIT", cache2)

	// Reset for whatever runs next.
	forceCacheInvalidation(t, baseURL, testUserID())
}

func testPutAfterCacheSetInvalidatesIt(t *testing.T, baseURL string) {
	// Populate the cache, then explicitly confirm it's actually set
	// (HIT) before trying to delete it — this is the "PUT after the
	// cache is set" case, not just "PUT at some point."
	getUser(t, baseURL, testUserID()) // MISS, populates
	_, cache, _ := getUser(t, baseURL, testUserID())
	require.Equal(t, "HIT", cache, "cache should be populated before testing invalidation")

	newSalary := forceCacheInvalidation(t, baseURL, testUserID())

	// The cache should now be gone: next GET must MISS and reflect the
	// new value, not whatever was cached before the update.
	status, cacheAfter, user := getUser(t, baseURL, testUserID())
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "MISS", cacheAfter)
	assert.Equal(t, newSalary, user.Salary)
}

// ---------- entry point ----------

func TestCacheBehaviorAcrossBackends(t *testing.T) {
	for _, env := range testEnvironments() {
		env := env
		t.Run(env.Name, func(t *testing.T) {
			// Deliberately not t.Parallel(): see the package comment above
			// re: both environments sharing one Postgres row.
			t.Run("GET_first_time_is_cache_miss", func(t *testing.T) {
				testGetIsCacheMissWhenNotCached(t, env.BaseURL)
			})
			t.Run("GET_second_time_is_cache_hit", func(t *testing.T) {
				testGetIsCacheHitOnSecondCall(t, env.BaseURL)
			})
			t.Run("PUT_after_cache_set_invalidates_it", func(t *testing.T) {
				testPutAfterCacheSetInvalidatesIt(t, env.BaseURL)
			})
		})
	}
}
