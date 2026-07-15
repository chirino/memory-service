# Build developer frontend
FROM node:26-alpine@sha256:e88a35be04478413b7c71c455cd9865de9b9360e1f43456be5951032d7ac1a66 AS frontend-builder
WORKDIR /build
COPY frontends/developer/package*.json ./
RUN npm ci
COPY frontends/developer/ ./
RUN npm run build

# Build Go binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.8@sha256:b471d69d4bf8a0aeac420f0e38777b1e38a59f7e805cba3e1c03d0066a1961af AS builder
USER 0
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG GO_BUILD_TAGS="sqlite_fts5 sqlite_json"
ARG VERSION=""
RUN CGO_ENABLED=1 go build -buildvcs=false -tags "${GO_BUILD_TAGS}" -ldflags "-X main.Version=${VERSION}" -o /memory-service .

# Runtime image
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:463cae32c6f6f5594b11a5c22de275016bd8545ce58a6373388e8b24f13fc15c
RUN microdnf install -y --nodocs \
    curl-minimal \
    sqlite-libs \
    libstdc++ \
    ca-certificates \
    && microdnf clean all \
    && mkdir -p /app /var/lib/memory-service/tmp \
    && chown -R 10001:10001 /app /var/lib/memory-service \
    && chmod 0700 /var/lib/memory-service/tmp
WORKDIR /app
COPY --from=builder --chown=10001:10001 /memory-service /memory-service
COPY --from=frontend-builder --chown=10001:10001 /build/dist /app/memory-service-developer
ENV MEMORY_SERVICE_DEVELOPER_FRONTEND_DIR=/app/memory-service-developer
ENV MEMORY_SERVICE_TEMP_DIR=/var/lib/memory-service/tmp
EXPOSE 8080
USER 10001:10001

ENTRYPOINT ["/memory-service", "serve"]
