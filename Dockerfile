FROM golang:1.26.1-bookworm AS builder
WORKDIR /src
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    pkg-config \
    libsqlite3-dev \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -buildvcs=false -tags "sqlite_fts5 sqlite_json" -o /memory-service .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    libsqlite3-0 \
    libstdc++6 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /memory-service /memory-service
EXPOSE 8080

# Map Fly.io's DATABASE_URL to the app's expected env var
ENTRYPOINT ["sh", "-c", "MEMORY_SERVICE_DB_URL=${MEMORY_SERVICE_DB_URL:-$DATABASE_URL} exec /memory-service serve"]
