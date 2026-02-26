//go:build tools

// this file is here so that `go mod download` will download the modules needed to build the project
package main

import (
	_ "github.com/4meepo/tagalign/cmd/tagalign"
	_ "github.com/go-task/task/v3/cmd/task"
	_ "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen"
	_ "google.golang.org/grpc/cmd/protoc-gen-go-grpc"
	_ "google.golang.org/protobuf/cmd/protoc-gen-go"
	_ "gotest.tools/gotestsum"
)
