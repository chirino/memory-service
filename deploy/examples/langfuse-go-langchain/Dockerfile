FROM golang:1.24-bookworm

WORKDIR /src
RUN git clone --depth 1 https://github.com/langfuse/langfuse-examples.git /src/langfuse-examples
WORKDIR /src/langfuse-examples/applications/golang-langchain-demo
RUN go mod download

ENV PORT=8080
EXPOSE 8080
CMD ["go", "run", "main.go"]
