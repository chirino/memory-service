# Build developer frontend
FROM node:26-alpine AS frontend-builder
WORKDIR /build
COPY frontends/developer/package*.json ./
RUN npm ci
COPY frontends/developer/ ./
RUN npm run build

# Build Go binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.8 AS builder
USER 0
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG GO_BUILD_TAGS="sqlite_fts5 sqlite_json"
RUN CGO_ENABLED=1 go build -buildvcs=false -tags "${GO_BUILD_TAGS}" -o /memory-service .

# Runtime image
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
RUN microdnf install -y --nodocs \
    curl-minimal \
    sqlite-libs \
    libstdc++ \
    ca-certificates \
    && microdnf clean all
WORKDIR /app
COPY --from=builder /memory-service /memory-service
COPY --from=frontend-builder /build/dist /app/memory-service-developer
ENV MEMORY_SERVICE_DEVELOPER_FRONTEND_DIR=/app/memory-service-developer
EXPOSE 8080

ENTRYPOINT ["/memory-service", "serve"]
