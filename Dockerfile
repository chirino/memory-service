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
ARG GO_BUILD_TAGS="sqlite_fts5 sqlite_json"
RUN CGO_ENABLED=1 go build -buildvcs=false -tags "${GO_BUILD_TAGS}" -o /memory-service .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    libsqlite3-0 \
    libstdc++6 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /memory-service /memory-service
EXPOSE 8080

ENTRYPOINT ["/memory-service", "serve"]
