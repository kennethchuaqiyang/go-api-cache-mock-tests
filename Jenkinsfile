pipeline {
    agent {
        docker {
            image 'golang:1.24'
        }
    }

    environment {
        // Defaults already point at the live Render deployments inside the
        // test file itself, so these are optional overrides. Left here so
        // it's obvious where they'd go if a different target is ever needed.
        // REDIS_BASE_URL   = 'https://redis-cache-mock-api.onrender.com'
        // INMEMORY_BASE_URL = 'https://inmemory-cache-mock-api.onrender.com'
        TEST_USER_ID = '2'
    }

    triggers {
        pollSCM('H/5 * * * *')
    }

    stages {
        stage('Install gotestsum') {
            steps {
                sh 'go install gotest.tools/gotestsum@latest'
            }
        }

        stage('Download dependencies') {
            steps {
                sh 'go mod download'
            }
        }

        stage('Run Redis + in-memory cache tests') {
            steps {
                // ELK tests deliberately excluded: they require a local
                // Elasticsearch + elk-cache-mock-api running, which this
                // Jenkins agent has no access to. Only the two live-Render
                // backends run here.
                sh '''
                    export PATH=$PATH:$(go env GOPATH)/bin
                    gotestsum --junitfile results.xml --format standard-verbose -- -run '^TestCacheBehaviorAcrossBackends$' -v ./...
                '''
            }
        }
    }

    post {
        always {
            junit 'results.xml'
        }
    }
}