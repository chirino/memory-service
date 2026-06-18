FROM registry.access.redhat.com/ubi9/go-toolset:1.25 AS builder
USER 0
WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG GO_BUILD_TAGS="sqlite_fts5 sqlite_json"
RUN CGO_ENABLED=1 go build -buildvcs=false -tags "${GO_BUILD_TAGS}" -o /memory-service .

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
RUN microdnf install -y --nodocs \
    curl-minimal \
    sqlite-libs \
    libstdc++ \
    ca-certificates \
    && microdnf clean all
COPY --from=builder /memory-service /memory-service
EXPOSE 8080

ENTRYPOINT ["/memory-service", "serve"]
