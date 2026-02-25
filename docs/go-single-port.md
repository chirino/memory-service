# Single-Port HTTP + gRPC (Plaintext + TLS) Multiplexing in Go

> Implemented in Go `serve` command on February 24, 2026.
> Implementation entrypoints:
> - `internal/cmd/serve/singleport.go`
> - `internal/cmd/serve/serve.go`

## Goal

Refactor a server that currently runs **separate ports** for HTTP and gRPC into a **single TCP port** that can accept:

- Plaintext HTTP/1.1
- Plaintext HTTP/2 (h2c)
- Plaintext gRPC (over h2c)
- TLS HTTP/1.1
- TLS HTTP/2
- TLS gRPC (HTTP/2 + ALPN)

…while keeping the “main” startup flow simple and pushing most complexity into helper functions.

## Non-Goals / Notes

- We are not trying to force clients into “best” transport; we merely accept what they can/will use.
- Plaintext HTTP/2 (h2c) is mainly useful for internal clients; browsers generally won’t do h2c.
- In production, it’s common to disable plaintext on public endpoints, but the design supports it.

---

## Dependencies

Recommended packages:

- `google.golang.org/grpc`
- `golang.org/x/net/http2`
- `golang.org/x/net/http2/h2c`
- `github.com/soheilhy/cmux` (for TLS-vs-plaintext splitting on one listener)

---

## Public API

### Configuration

```go
type ListenerConfig struct {
    EnablePlainText bool // default true
    EnableTLS       bool // default true

    // Port to bind. If 0, OS chooses an ephemeral port.
    Port int // default 0

    // Optional TLS material. If not provided and EnableTLS is true,
    // generate an in-memory self-signed cert.
    TLSCertFile string
    TLSKeyFile  string

    // Optional: If you prefer supplying cert bytes directly:
    // TLSCertPEM []byte
    // TLSKeyPEM  []byte

    // Optional tuning
    ReadHeaderTimeout time.Duration // reasonable default, e.g. 5s
}
````

Rules:

* Error if `!EnablePlainText && !EnableTLS`.
* If `EnableTLS == true` and no certs provided: generate self-signed.
* Expose the actual bound port even if `Port == 0`.

### Start Function

```go
type RunningServers struct {
    Addr        net.Addr  // bound address from the base listener
    Port        int       // parsed from Addr
    Close       func(ctx context.Context) error
    // Optional: expose underlying servers for metrics/debug
    HTTPServerPlain *http.Server
    HTTPServerTLS   *http.Server
    GRPCServer      *grpc.Server
}

func StartSinglePortHTTPAndGRPC(
    ctx context.Context,
    cfg ListenerConfig,
    httpHandler http.Handler, // your router
    grpcServer *grpc.Server,  // already registered services
    logger Logger,            // your logging abstraction (or nil)
) (*RunningServers, error)
```

`StartSinglePortHTTPAndGRPC` should:

* Bind the TCP port once.
* Set up multiplexing.
* Start serving in goroutines.
* Provide a single `Close(ctx)` to gracefully stop everything.

---

## High-Level Architecture

### Layer 1: TCP Multiplexing (TLS vs Plaintext)

We bind exactly one `net.Listener` and use `cmux` to split connections by first bytes:

* TLS connections are identified by the TLS ClientHello.
* Non-TLS connections are treated as plaintext.

This allows one port to accept both `https://` and `http://` traffic.

### Layer 2: HTTP-level Dispatch (gRPC vs “normal” HTTP)

Within both the TLS and plaintext HTTP servers, route requests by inspecting:

* HTTP/2 requirement: `r.ProtoMajor == 2`
* gRPC marker: `Content-Type` starts with `application/grpc` (or contains it)

If both match => dispatch to `grpcServer.ServeHTTP(w,r)`
Else => dispatch to `httpHandler.ServeHTTP(w,r)`

### Plaintext HTTP/2 (h2c)

For plaintext, wrap the dispatch handler with `h2c.NewHandler(...)` so it can speak:

* HTTP/1.1
* h2c Upgrade
* h2c prior-knowledge (HTTP/2 preface)

### TLS HTTP/2 via ALPN

For TLS, use a standard `http.Server` with TLS enabled.
Go will negotiate ALPN (`h2` / `http/1.1`) automatically when configured normally.

---

## Request Dispatch Function

A single function shared by both servers:

```go
func grpcOrHTTPHandler(grpcServer *grpc.Server, httpHandler http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // gRPC is HTTP/2 + application/grpc
        ct := r.Header.Get("Content-Type")
        if r.ProtoMajor == 2 && strings.HasPrefix(ct, "application/grpc") {
            grpcServer.ServeHTTP(w, r)
            return
        }
        httpHandler.ServeHTTP(w, r)
    })
}
```

Notes:

* Some gRPC variants include suffixes like `application/grpc+proto`, `application/grpc+json`.
  Consider using `strings.HasPrefix(ct, "application/grpc")` (recommended).
* Do not attempt to route gRPC by URL path; content-type is the canonical signal.

---

## Listener Binding and Port Discovery

Bind once:

```go
baseLis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
```

Then get actual port:

```go
tcpAddr := baseLis.Addr().(*net.TCPAddr)
actualPort := tcpAddr.Port
```

Return that in `RunningServers.Port`.

---

## TLS Material Handling

### If cert/key provided (files)

Load using `tls.LoadX509KeyPair`.

### If not provided and TLS enabled

Generate an in-memory self-signed cert:

* Generate RSA/ECDSA key
* Create x509 cert with:

  * CN like `localhost`
  * SANs: `localhost`, `127.0.0.1`, `::1`
  * validity like 30–365 days
* Use the resulting `tls.Certificate` in `tls.Config{Certificates: []tls.Certificate{cert}}`

This avoids requiring filesystem writes.

---

## Multiplexing Implementation Details

### cmux setup (conceptual)

```go
m := cmux.New(baseLis)

// TLS connections (ClientHello)
tlsLis := m.Match(cmux.TLS())

// Plaintext: HTTP/1.1 or HTTP/2 prior-knowledge (h2c)
plainLis := m.Match(cmux.Any()) // or more precise matches if desired
```

We keep it simple: cmux first match TLS, everything else goes plaintext.

### Plaintext Server

Use an `http.Server` with handler wrapped by `h2c.NewHandler`:

```go
h := grpcOrHTTPHandler(grpcServer, httpHandler)
h2s := &http2.Server{}
plainHandler := h2c.NewHandler(h, h2s)

plainHTTP := &http.Server{
    Handler: plainHandler,
    ReadHeaderTimeout: cfg.ReadHeaderTimeout,
}
go plainHTTP.Serve(plainLis)
```

### TLS Server

TLS must be terminated before HTTP routing.
Use `Serve` on a TLS-wrapped listener:

```go
tlsCfg := &tls.Config{
    Certificates: []tls.Certificate{cert},
    NextProtos:   []string{"h2", "http/1.1"}, // explicit is ok; Go often sets h2 automatically
}
tlsWrapped := tls.NewListener(tlsLis, tlsCfg)

tlsHTTP := &http.Server{
    Handler: grpcOrHTTPHandler(grpcServer, httpHandler),
    ReadHeaderTimeout: cfg.ReadHeaderTimeout,
}
go tlsHTTP.Serve(tlsWrapped)
```

### Start cmux

```go
go func() {
    _ = m.Serve() // exits when base listener closed
}()
```

---

## Startup Flow (Desired Simplicity)

Your main program becomes:

```go
grpcServer := grpc.NewServer()
pb.RegisterSystemServiceServer(grpcServer, &grpcserver.SystemServer{})

running, err := StartSinglePortHTTPAndGRPC(
    ctx,
    ListenerConfig{ /* defaults */ },
    router,
    grpcServer,
    log,
)
if err != nil { ... }

log.Info("server listening", "port", running.Port)
```

No separate goroutines or separate ports in main.

---

## Shutdown / Graceful Close

Provide one `Close(ctx)` that:

1. Calls `grpcServer.GracefulStop()` (or `Stop()` on timeout)
2. Calls `plainHTTP.Shutdown(ctx)` if plaintext enabled
3. Calls `tlsHTTP.Shutdown(ctx)` if TLS enabled
4. Closes base listener (which will unwind cmux.Serve)

Implementation outline:

```go
closeFn := func(ctx context.Context) error {
    // stop gRPC (graceful)
    done := make(chan struct{})
    go func() { grpcServer.GracefulStop(); close(done) }()
    select {
    case <-done:
    case <-ctx.Done():
        grpcServer.Stop()
    }

    var err1, err2 error
    if plainHTTP != nil { err1 = plainHTTP.Shutdown(ctx) }
    if tlsHTTP != nil { err2 = tlsHTTP.Shutdown(ctx) }
    _ = baseLis.Close()

    return errors.Join(err1, err2)
}
```

---

## Validation and Errors

At the start of `StartSinglePortHTTPAndGRPC`:

* If both disabled => return error.
* If TLS enabled but cert invalid/unloadable => return error.
* If `net.Listen` fails => return error.

---

## Observability

Optional but recommended:

* Log which modes are enabled:

  * “plaintext enabled”
  * “TLS enabled (self-signed)” vs “TLS enabled (provided certs)”
* Log final bound port.
* Expose a health endpoint on the HTTP router.

---

## Security Considerations

* If `EnablePlainText` is true on a public interface, clients may use plaintext accidentally.
* Consider:

  * defaulting plaintext to true only for local/dev,
  * binding to `127.0.0.1` in dev, or
  * redirecting plaintext HTTP/1.1 to HTTPS (note: redirects don’t apply to gRPC/h2c cleanly).

---

## Testing Plan

1. Plaintext HTTP/1.1:

   * `curl -v http://localhost:<port>/health`
2. TLS HTTP/1.1:

   * `curl -vk https://localhost:<port>/health`
3. TLS HTTP/2:

   * `curl -vk --http2 https://localhost:<port>/health`
4. TLS gRPC:

   * `grpcurl -insecure localhost:<port> list`
5. Plaintext h2c (if needed):

   * use a Go client or `grpcurl -plaintext localhost:<port> list`
   * for HTTP/2 h2c, a Go `http2.Transport` with `AllowHTTP: true` + `DialTLS` override

---

## Implementation Checklist

* [x] Add `ListenerConfig` with defaults.
* [x] Implement `grpcOrHTTPHandler`.
* [x] Implement `loadOrGenerateTLSCert`.
* [x] Implement `StartSinglePortHTTPAndGRPC`.
* [x] Update service startup to bind one port and log returned `running.Port`.
* [ ] Add smoke tests / scripts for curl + grpcurl.
