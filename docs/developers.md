# Development Tips

## Required Development Tools

### Core Runtimes

| Tool | Install |
|------|---------|
| [Go](https://go.dev/dl/) (1.26+) | https://go.dev/dl/ |
| [Node.js](https://nodejs.org/) (v20+) | https://nodejs.org/ or `brew install node` |
| [Java 21 (Temurin)](https://adoptium.net/) | https://adoptium.net/ or `brew install --cask temurin@21` |
| [Docker](https://docs.docker.com/get-docker/) + Compose | https://docs.docker.com/get-docker/ |

### Build & Task Runner

| Tool | Install |
|------|---------|
| [task](https://taskfile.dev/installation/) — project task runner | `brew install go-task` or https://taskfile.dev/installation/ |

### Go Dev Tools

| Tool | Install |
|------|---------|
| [air](https://github.com/air-verse/air) — hot reload for `task dev:memory-service` | `go install github.com/cosmtrek/air@v1.51.0` |
| [gotestsum](https://github.com/gotestyourself/gotestsum) — test runner used by `task test:go` | `go install gotest.tools/gotestsum@latest` |
| [dlv](https://github.com/go-delve/delve) — Go debugger | `go install github.com/go-delve/delve/cmd/dlv@latest` |

> **Note:** Code generation tools (`oapi-codegen`, `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`, `tagalign`) are auto-installed by `go generate ./...` — no manual setup needed.

### API Testing

| Tool | Install |
|------|---------|
| [jq](https://jqlang.github.io/jq/) — JSON processor | `brew install jq` |
| [grpcurl](https://github.com/fullstorydev/grpcurl) — gRPC curl | `brew install grpcurl` |

### Kubernetes (optional — needed for `task kind:*`)

| Tool | Install |
|------|---------|
| [kind](https://kind.sigs.k8s.io/) — local k8s clusters | `brew install kind` |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | `brew install kubectl` |
| [kustomize](https://kubectl.docs.kubernetes.io/installation/kustomize/) | `brew install kustomize` |

### Python (optional — needed for Python SDK work)

| Tool | Install |
|------|---------|
| [uv](https://docs.astral.sh/uv/) — Python package manager | `curl -LsSf https://astral.sh/uv/install.sh \| sh` |

### GitHub CLI (optional)

| Tool | Install |
|------|---------|
| [gh](https://cli.github.com/) — GitHub CLI | `brew install gh` |



## Running agaist a local LLM like LM Studio

Before running the chat example apps:

```bash
export OPENAI_BASE_URL=http://localhost:1234
```

or if tour chat app is running in a docker container:

```bash
export OPENAI_BASE_URL=http://host.docker.internal:1234
```

## Running chat-quarkus in dev mode.

Run the chat-quarkus in dev mode, but firsts compile it's dependencies:
```bash

./java/mvnw -T 1C -f java/pom.xml install -pl ':chat-quarkus' -am -DskipTests && \
    docker compose build memory-service && \
    ./java/mvnw -T 1C -f java/pom.xml -pl :chat-quarkus quarkus:dev
```

The above handles starting all depdencies including the memory-service in containers.

## Running memeory-service and chat-quarkus in dev mode.

Run the memory-service in dev mode:
```bash
task dev:memory-service
```

The above handles starting all depdencies of the memory-service in containers.

Run the chat-quarkus in dev mode, but firsts compile it's dependencies..
```bash
./java/mvnw -T 1C -f java/pom.xml install -pl ':memory-service-extension-deployment' -am -DskipTests && \
    ./java/mvnw -T 1C -f java/pom.xml -pl :chat-quarkus quarkus:dev -Dquarkus.profile=alt
```

## Running memeory-service and chat-string /w docker compose 

```bash
./java/mvnw -DskipTests -f java/pom.xml -am -pl :memory-service-spring-boot-docker-compose-starter clean install && ./java/mvnw -f java/pom.xml -pl :chat-spring spring-boot:run
```

If you don't want it to start the memory-service in a container on boot then run this before starting it:

```bash
export SPRING_DOCKER_COMPOSE_ENABLED=false
```

###

Testing the APIs with curl

Setup a function that will give you a bearer token:

```bash
function get-token() {
    curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "client_id=memory-service-client" \
    -d "client_secret=change-me" \
    -d "grant_type=password" \
    -d "username=alice" \
    -d "password=alice" \
    | jq -r '.access_token'
}
```

```bash
curl -sSfX GET "http://localhost:8082/v1/admin/stats/store-latency-p95" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" | jq
```

## Testing in Kubernetes

Requirments:

* kind
* task

To create a new kind cluster running the memory-service and the chat-quarkus demo, run:

```bash
task kind:reset
```

Run `task --list` to see all available targets that you can use to manage the kind deployment.
