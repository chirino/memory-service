# Build developer frontend
FROM node:26.10.0-alpine@sha256:0b36e8c136b94cd4fcf02188228e76c31ad5872eef3fec8cbd2eee500cfd9e80 AS frontend-builder
WORKDIR /build
COPY frontends/developer/package*.json ./
RUN npm ci
COPY frontends/developer/ ./
RUN npm run build

# Build Go binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.8@sha256:60fd6fb2998c40ad0d6561c34e83588b986a0cf7771f64e5e5c295ba87101f9b AS builder
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
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:c4c285d42941ebb2bd4da878479008e1e4536bb599aad1dbe568f89de9053661
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
COPY --chown=10001:10001 deploy/clickhouse/projections/ /etc/memory-service/clickhouse/projections/
ENV MEMORY_SERVICE_DEVELOPER_FRONTEND_DIR=/app/memory-service-developer
ENV MEMORY_SERVICE_POLICY_IMPORT_PATH=/etc/memory-service/policies/
ENV MEMORY_SERVICE_TEMP_DIR=/var/lib/memory-service/tmp
EXPOSE 8080
USER 10001:10001

ENTRYPOINT ["/memory-service", "serve"]
