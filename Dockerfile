# Build developer frontend
FROM node:26-alpine@sha256:2d984a15c9b54fd0aeb608b8e0d0d83529eb34d2966db27a1fb4f1edc3d298a3 AS frontend-builder
WORKDIR /build
COPY frontends/developer/package*.json ./
RUN npm ci
COPY frontends/developer/ ./
RUN npm run build

# Build Go binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.8@sha256:5e68f09a652ac6627a83c57655e42e24575efb278b54336039c9308607fc6b21 AS builder
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
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:7fbeae18dc9476399f565e68255f602a3374ea8614ba3d14843565131a13ff93
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
ENV MEMORY_SERVICE_POLICY_IMPORT_DIR=/etc/memory-service/policies/
ENV MEMORY_SERVICE_TEMP_DIR=/var/lib/memory-service/tmp
EXPOSE 8080
USER 10001:10001

ENTRYPOINT ["/memory-service", "serve"]
