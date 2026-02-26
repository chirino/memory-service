// Package main orchestrates all code generation for the memory service.
// Run via: go generate ./...
// This generates:
//   - OpenAPI types + server interfaces (oapi-codegen) for Agent and Admin APIs
//   - gRPC stubs (protoc) for memory_service.proto
package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const protocVersion = "34.0"

func main() {
	root := findProjectRoot()

	// Ensure tools are installed
	installTools(root)

	// Generate OpenAPI types + server interfaces
	generateOpenAPI(root)

	// Generate gRPC stubs
	generateGRPC(root)

	// Align struct tags in generated Go code
	fmt.Println("Aligning struct tags...")
	run("go", "run", "github.com/4meepo/tagalign/cmd/tagalign", "-fix", "-sort", "-order", "json,gorm,enum,example", "./internal/...")

	// Format all generated Go code
	fmt.Println("Formatting generated Go code...")
	run("gofmt", "-w", filepath.Join(root))

	fmt.Println("Code generation complete.")
}

func installTools(root string) {
	fmt.Println("Ensuring codegen tools are installed...")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create bin dir: %v\n", err)
		os.Exit(1)
	}
	// Build protoc plugins into bin/ so protoc can find them via --plugin flags.
	run("go", "build", "-o", filepath.Join(binDir, "protoc-gen-go"), "google.golang.org/protobuf/cmd/protoc-gen-go")
	run("go", "build", "-o", filepath.Join(binDir, "protoc-gen-go-grpc"), "google.golang.org/grpc/cmd/protoc-gen-go-grpc")
	installProtoc(root)
}

func installProtoc(root string) {
	binDir := filepath.Join(root, "bin")
	protocBin := filepath.Join(binDir, "protoc")

	// Check if the correct version is already installed
	if isProtocVersionInstalled(protocBin, protocVersion) {
		fmt.Printf("protoc %s already installed.\n", protocVersion)
		return
	}

	fmt.Printf("Installing protoc %s to %s...\n", protocVersion, binDir)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create bin dir: %v\n", err)
		os.Exit(1)
	}

	url := protocDownloadURL(protocVersion)
	fmt.Printf("Downloading %s...\n", url)
	downloadAndExtractProtoc(url, binDir)
	fmt.Printf("protoc %s installed to %s\n", protocVersion, protocBin)
}

func isProtocVersionInstalled(protocBin, version string) bool {
	if _, err := os.Stat(protocBin); os.IsNotExist(err) {
		return false
	}
	out, err := exec.Command(protocBin, "--version").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), version)
}

func protocDownloadURL(version string) string {
	var osName, arch string
	switch runtime.GOOS {
	case "darwin":
		osName = "osx"
	case "linux":
		osName = "linux"
	default:
		fmt.Fprintf(os.Stderr, "unsupported OS for protoc install: %s\n", runtime.GOOS)
		os.Exit(1)
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch_64"
	default:
		fmt.Fprintf(os.Stderr, "unsupported arch for protoc install: %s\n", runtime.GOARCH)
		os.Exit(1)
	}
	return fmt.Sprintf(
		"https://github.com/protocolbuffers/protobuf/releases/download/v%s/protoc-%s-%s-%s.zip",
		version, version, osName, arch,
	)
}

// downloadAndExtractProtoc downloads the protoc zip and extracts:
//   - bin/protoc      → binDir/protoc
//   - include/...     → binDir/include/... (well-known proto types)
func downloadAndExtractProtoc(url, binDir string) {
	resp, err := http.Get(url) //nolint:gosec // URL is constructed from a pinned const
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to download protoc: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "failed to download protoc: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}

	// Write to a temp file
	tmp, err := os.CreateTemp("", "protoc-*.zip")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write protoc zip: %v\n", err)
		os.Exit(1)
	}
	tmp.Close()

	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open protoc zip: %v\n", err)
		os.Exit(1)
	}
	defer zr.Close()

	gotProtoc := false
	for _, f := range zr.File {
		var destPath string
		switch {
		case f.Name == "bin/protoc":
			destPath = filepath.Join(binDir, "protoc")
		case strings.HasPrefix(f.Name, "include/") && !f.FileInfo().IsDir():
			// e.g. include/google/protobuf/empty.proto → binDir/include/google/protobuf/empty.proto
			destPath = filepath.Join(binDir, filepath.FromSlash(f.Name))
		default:
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create dir for %s: %v\n", destPath, err)
			os.Exit(1)
		}

		rc, err := f.Open()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open %s in zip: %v\n", f.Name, err)
			os.Exit(1)
		}

		perm := os.FileMode(0o644)
		if f.Name == "bin/protoc" {
			perm = 0o755
			gotProtoc = true
		}
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
		if err != nil {
			rc.Close()
			fmt.Fprintf(os.Stderr, "failed to create %s: %v\n", destPath, err)
			os.Exit(1)
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", destPath, copyErr)
			os.Exit(1)
		}
	}

	if !gotProtoc {
		fmt.Fprintf(os.Stderr, "bin/protoc not found in downloaded zip\n")
		os.Exit(1)
	}
}

func generateOpenAPI(root string) {
	contractsDir := filepath.Join(root, "memory-service-contracts", "src", "main", "resources")

	// Agent API
	agentSpec := filepath.Join(contractsDir, "openapi.yml")
	agentCfg := filepath.Join(root, "internal", "generated", "api", "cfg.yaml")
	agentOut := filepath.Join(root, "internal", "generated", "api")
	os.MkdirAll(agentOut, 0o755)
	fmt.Println("Generating Agent API types + server interfaces...")
	run("go", "run", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen", "--config="+agentCfg, agentSpec)

	// Admin API
	adminSpec := filepath.Join(contractsDir, "openapi-admin.yml")
	adminCfg := filepath.Join(root, "internal", "generated", "admin", "cfg.yaml")
	adminOut := filepath.Join(root, "internal", "generated", "admin")
	os.MkdirAll(adminOut, 0o755)
	fmt.Println("Generating Admin API types + server interfaces...")
	run("go", "run", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen", "--config="+adminCfg, adminSpec)
}

func generateGRPC(root string) {
	protoFile := filepath.Join(root, "memory-service-contracts", "src", "main", "resources", "memory", "v1", "memory_service.proto")
	pbOut := filepath.Join(root, "internal", "generated", "pb")
	os.MkdirAll(pbOut, 0o755)

	protoInclude := filepath.Join(root, "memory-service-contracts", "src", "main", "resources")
	binDir := filepath.Join(root, "bin")
	protocBin := filepath.Join(binDir, "protoc")
	wellKnownIncludes := filepath.Join(binDir, "include")

	fmt.Println("Generating gRPC stubs...")
	run(protocBin,
		"--proto_path="+protoInclude,
		"--proto_path="+wellKnownIncludes,
		"--plugin=protoc-gen-go="+filepath.Join(binDir, "protoc-gen-go"),
		"--plugin=protoc-gen-go-grpc="+filepath.Join(binDir, "protoc-gen-go-grpc"),
		"--go_out="+pbOut, "--go_opt=paths=source_relative",
		"--go-grpc_out="+pbOut, "--go-grpc_opt=paths=source_relative",
		protoFile,
	)
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "command failed: %s %v: %v\n", name, args, err)
		os.Exit(1)
	}
}

func findProjectRoot() string {
	// Walk up from the executable's directory to find go.mod
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot get working directory: %v\n", err)
		os.Exit(1)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fmt.Fprintf(os.Stderr, "cannot find project root (go.mod)\n")
			os.Exit(1)
		}
		dir = parent
	}
}
