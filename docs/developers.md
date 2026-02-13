# Development Tips

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

./mvnw -T 1C install -pl ':chat-quarkus' -am -DskipTests && \
    docker compose build memory-service && \
    ./mvnw -T 1C -pl :chat-quarkus quarkus:dev
```

The above handles starting all depdencies including the memory-service in containers.

## Running memeory-service and chat-quarkus in dev mode.

Run the memory-service in dev mode, but first compile it's dependencies..
```bash
./mvnw -T 1C install -pl ':memory-service' -am -DskipTests && \
    ./mvnw -T 1C -pl :memory-service quarkus:dev
```

The above handles starting all depdencies of the memory-service in containers.

Run the chat-quarkus in dev mode, but firsts compile it's dependencies..
```bash
./mvnw -T 1C install -pl ':memory-service-extension-deployment' -am -DskipTests && \
    ./mvnw -T 1C -pl :chat-quarkus quarkus:dev -Dquarkus.profile=alt
```

## Running memeory-service and chat-string /w docker compose 

```bash
./mvnw -DskipTests -am -pl :memory-service-spring-boot-docker-compose-starter clean install && ./mvnw -pl :chat-spring spring-boot:run
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
