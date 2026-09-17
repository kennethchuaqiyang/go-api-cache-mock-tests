# go-api-cache-mock-tests

Go API automation covering [redis-cache-mock-api](https://github.com/kennethchuaqiyang/redis-cache-mock-api) and [inmemory-cache-mock-api](https://github.com/kennethchuaqiyang/inmemory-cache-mock-api).

The equivalent test file for [elk-cache-mock-api](https://github.com/kennethchuaqiyang/elk-cache-mock-api) (`cache_elk_test.go`) is **not** in this repo — it's kept as a standalone backup in [elk-cache-tests-backup](https://github.com/kennethchuaqiyang/elk-cache-tests-backup), since ELK runs locally-only and isn't part of this repo's CI story.

**CI:** [Jenkins job](http://localhost:8080/job/go-api-cache-mock-tests/) — on every SCM poll.

## Test cases

`cache_redis_inmemory_test.go` is table-driven across both backends (`redis`, `inmemory`), running the same three cases per environment:

1. **GET, first time / not cached → MISS.** Ends by forcing a genuine update, so the cache is guaranteed empty for whatever runs next — not dependent on the TTL having expired by then.
2. **GET, second time within TTL → HIT.** Same cleanup at the end.
3. **PUT after the cache is confirmed set (HIT) → invalidates it.** The next GET must MISS and reflect the new value.

### The "force a real update" problem

The update endpoint returns `{"message": "No update"}` if the new salary happens to equal the current DB value — a no-op, not a real test action. `forceCacheInvalidation()` handles this: it reads the current salary, adds an increasing offset, and retries until it gets back `"Success"`, rather than silently treating a coincidental no-op as if the test had done something.

### Why Redis and in-memory run sequentially, not in parallel

Both point at the **same underlying Postgres row** for the test user (they're separate cache layers in front of one database). Running them concurrently could let one environment's `PUT` race against the other's `GET` assertion, so neither environment subtest uses `t.Parallel()`.

## Environment variables

| Var | Default |
|---|---|
| `REDIS_BASE_URL` | `https://redis-cache-mock-api.onrender.com` |
| `INMEMORY_BASE_URL` | `https://inmemory-cache-mock-api.onrender.com` |
| `TEST_USER_ID` | `2` |

All optional — override to point at local instances during development.

## Running locally

```bash
go mod tidy
go test ./... -v -run '^TestCacheBehaviorAcrossBackends$'
```

## Running the ELK tests

Not part of this repo — see [elk-cache-tests-backup](https://github.com/kennethchuaqiyang/elk-cache-tests-backup) for `cache_elk_test.go` and its own run instructions (requires a local Elasticsearch + `elk-cache-mock-api`).

## CI

The Jenkins pipeline (`golang:1.24` Docker agent) runs `TestCacheBehaviorAcrossBackends`. Results are published via `gotestsum` → JUnit XML.