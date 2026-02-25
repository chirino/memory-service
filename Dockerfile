FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /memory-service .

FROM alpine:3.21
RUN apk add --no-cache curl
COPY --from=builder /memory-service /memory-service
EXPOSE 8080
ENTRYPOINT ["/memory-service", "serve"]
