# Development Tips

## Running chat-quarkus in dev mode.

Run the chat-quarkus in dev mode, but firsts compile it's dependencies:
```bash

./mvnw -T 1C clean install -pl '!:chat-quarkus' -am -DskipTests && \
    docker compose build memory-service && \
    ./mvnw -T 1C -pl :chat-quarkus quarkus:dev
```

The above handles starting all depdencies including the memory-service in containers.

## Running memeory-service and chat-quarkus in dev mode.

Run the memory-service in dev mode, but first compile it's dependencies..
```bash
./mvnw -T 1C clean install -pl '!:memory-service' -am -DskipTests && \
    ./mvnw -T 1C -pl :memory-service quarkus:dev
```

The above handles starting all depdencies of the memory-service in containers.

Run the chat-quarkus in dev mode, but firsts compile it's dependencies..
```bash
./mvnw -T 1C clean install -pl '!:chat-quarkus' -am -DskipTests && \
    ./mvnw -T 1C -pl :chat-quarkus quarkus:dev -Dquarkus.profile=alt
```
