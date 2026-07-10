# Workarounds

## sqlite-vec musl typedef aliases

- What: `Dockerfile.portable` builds the static Linux binary with `CGO_CFLAGS="-Du_int8_t=uint8_t -Du_int16_t=uint16_t -Du_int64_t=uint64_t"` while compiling sqlite-vec.
- Why: `github.com/asg017/sqlite-vec-go-bindings` v0.1.6 references BSD `u_int*` aliases in its bundled C source. musl does not define those aliases, so the static Alpine/musl build fails even though the same code builds on glibc.
- Proper fix: Upgrade sqlite-vec/go bindings once they avoid BSD-only typedef assumptions, or carry a small upstreamable patch in the dependency instead of passing preprocessor aliases from the build.

## Spring REST UDS HTTP/1.1 Forcing

- What: `java/spring/memory-service-rest-spring` uses a custom `UnixDomainSocketClientHttpConnector` and reflectively forces Reactor Netty's outbound request to `HTTP/1.1` for Unix-domain-socket REST calls.
- Why: With direct UDS transport, Reactor Netty was emitting `HTTP/3.0` request versions on the socket, and the Go memory-service listener rejected them with `505 HTTP Version Not Supported`.
- Proper fix: Replace the reflective override with a supported Reactor Netty/Spring WebFlux configuration path that guarantees `HTTP/1.1` over UDS, or move Spring REST UDS to a client stack that exposes first-class Unix-socket + HTTP-version control without reflection.

## Mongo GridFS Concurrent Upload Lock

- What: `internal/plugin/attach/mongostore/MongoAttachmentStore.Store` now takes a mutex before calling the shared Mongo `GridFSBucket` upload path.
- Why: `go.mongodb.org/mongo-driver/v2` races inside `GridFSBucket`'s first-write/index-creation path under concurrent attachment uploads, which caused `task test:go -race` failures and corrupted BDD attachment contents in the Mongo backend.
- Proper fix: Replace the store-side lock with a driver-level fix or a safe one-time GridFS index/bootstrap path that avoids shared mutable `GridFSBucket` state during concurrent uploads. I've reported the issue at https://jira.mongodb.org/browse/GODRIVER-3841 

## GitHub Cucumber Annotation Timeout

- What: The optional `deblockt/cucumber-report-annotations-action@v1.21` CI step has a 5-minute timeout while keeping `continue-on-error: true`.
- Why: The action can hang after test execution, artifact upload, and generated Cucumber report files have already succeeded, leaving an otherwise complete matrix job stuck in progress.
- Proper fix: Replace the third-party annotation action with a maintained reporting path or an in-repo summary generator that cannot block required CI jobs indefinitely.
