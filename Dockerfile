# Build developer frontend
FROM node:26-alpine@sha256:2d984a15c9b54fd0aeb608b8e0d0d83529eb34d2966db27a1fb4f1edc3d298a3 AS frontend-builder
WORKDIR /build
COPY frontends/developer/package*.json ./
RUN npm ci
COPY frontends/developer/ ./
RUN npm run build

# Build Go binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.8@sha256:0a4666f7a4eb0644c97a73cba198eb268691b270d97831822689e7a2088f87be AS builder
USER 0
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG GO_BUILD_TAGS="sqlite_fts5 sqlite_json"
ARG VERSION=""
# The build context may lack usable .git metadata (git worktrees, trimmed contexts),
# so VCS stamping stays disabled; otherwise go build fails with "error obtaining VCS status".
RUN CGO_ENABLED=1 go build -buildvcs=false -tags "${GO_BUILD_TAGS}" -ldflags "-X main.Version=${VERSION}" -o /memory-service .

# Runtime image. Keep it glibc-based: sqlite-vec does not compile cleanly against musl
# without extra CFLAGS shims; the static musl build lives in Dockerfile.portable.
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:8ebe2ad8fdf3cab3e5a53c1edc69194c98209cfadab24b884f4ad9ebcf7bbbfc
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
COPY --chown=10001:10001 deploy/episodic-policies/ /etc/memory-service/policies/
ENV MEMORY_SERVICE_DEVELOPER_FRONTEND_DIR=/app/memory-service-developer
ENV MEMORY_SERVICE_POLICY_IMPORT_PATH=/etc/memory-service/policies/
ENV MEMORY_SERVICE_TEMP_DIR=/var/lib/memory-service/tmp
EXPOSE 8080
USER 10001:10001

ENTRYPOINT ["/memory-service", "serve"]
