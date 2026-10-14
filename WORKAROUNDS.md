# Workarounds

## SQLite timestamp pairs

- What: SQLite stores each timestamp as integer Unix seconds in `*_at` and normalized nanoseconds in `*_at_ns`. SQLite-only GORM builders expand model writes and projections. Raw SQL supplies both columns and two adjacent placeholders for each timestamp argument. The driver expands that argument, validates the resulting parameter count, and merges paired result columns into UTC dates. Nullable raw SQL timestamps accept `*time.Time` and `sql.NullTime`. Nil pointers and invalid `sql.NullTime` values bind two NULL parameters. Named timestamp parameters are unsupported. Bound parameters must belong to the first statement in a SQL batch.
- Why: go-sqlite3 binds dates as text and interprets integer DATETIME values as seconds or milliseconds. A single signed integer of Unix nanoseconds cannot represent all accepted API dates. Paired integers preserve the existing date range and nanosecond precision.
- Proper fix: Add paired timestamp binding and result decoding to go-sqlite3 or GORM, then replace the local adapter.

## gRPC Java Unix socket authority

- What: The Spring and Quarkus Unix socket gRPC channel builders set the HTTP/2 authority to `localhost`.
- Why: gRPC Java otherwise uses the socket path as `:authority`. The Go h2c listener rejects that value with `invalid_authority`, so response recording and resumption fail.
- Proper fix: Remove the overrides after gRPC Java supplies a valid default authority for Unix socket addresses, and verify both Unix socket site scenarios.

## gRPC vulnerability database version mismatch

- What: Keep `google.golang.org/grpc` at v1.83.2 and `google.golang.org/api` at v0.298.0. The newer API version selects gRPC v1.84.0.
- Why: The [gRPC maintainer advisory](https://github.com/grpc/grpc-go/security/advisories/GHSA-2v4p-qf9q-27wj) lists both v1.83.2 and v1.84.0 as fixed for CVE-2026-84445, but the Go vulnerability database currently flags v1.84.0 and makes `govulncheck ./...` fail.
- Proper fix: Remove the hold when the Go vulnerability database corrects the affected range or a newer stable gRPC release passes `govulncheck`, then update both modules together.

## Maven compiler plugin 3.14.1 pin

- What: `java/pom.xml` pins `maven-compiler-plugin` to 3.14.1 instead of 3.15.0.
- Why: In this reactor, the 3.15.0 incremental rebuild path deletes protobuf-generated Java sources before compilation and removes copied `META-INF/spring` auto-configuration metadata from `target/classes`. The first failure breaks `memory-service-proto-spring` compilation on an incremental reactor run. The second lets compilation finish but makes `chat-spring` fail because `MemoryServiceClientProperties` is never registered.
- Proper fix: Upgrade after a compiler-plugin release preserves generated source roots and non-class resources during incremental rebuilds, then verify with `task test:java`.

## sqlite-vec musl typedef aliases

- What: `Dockerfile.portable` builds the static Linux binary with `CGO_CFLAGS="-Du_int8_t=uint8_t -Du_int16_t=uint16_t -Du_int64_t=uint64_t"` while compiling sqlite-vec.
- Why: `github.com/asg017/sqlite-vec-go-bindings` v0.1.6 references BSD `u_int*` aliases in its bundled C source. musl does not define those aliases, so the static Alpine/musl build fails even though the same code builds on glibc.
- Proper fix: Upgrade sqlite-vec/go bindings once they avoid BSD-only typedef assumptions, or carry a small upstreamable patch in the dependency instead of passing preprocessor aliases from the build.

## Spring REST UDS HTTP/1.1 Forcing

- What: `java/spring/memory-service-rest-spring` uses a custom `UnixDomainSocketClientHttpConnector` and reflectively forces Reactor Netty's outbound request to `HTTP/1.1` for Unix-domain-socket REST calls.
- Why: With direct UDS transport, Reactor Netty was emitting `HTTP/3.0` request versions on the socket, and the Go memory-service listener rejected them with `505 HTTP Version Not Supported`.
- Proper fix: Replace the reflective override with a supported Reactor Netty/Spring WebFlux configuration path that guarantees `HTTP/1.1` over UDS, or move Spring REST UDS to a client stack that exposes first-class Unix-socket + HTTP-version control without reflection.

## GitHub Cucumber Annotation Timeout

- What: The optional `deblockt/cucumber-report-annotations-action@v1.21` CI step has a 5-minute timeout while keeping `continue-on-error: true`.
- Why: The action can hang after test execution, artifact upload, and generated Cucumber report files have already succeeded, leaving an otherwise complete matrix job stuck in progress.
- Proper fix: Replace the third-party annotation action with a maintained reporting path or an in-repo summary generator that cannot block required CI jobs indefinitely.

## Chat client nullable title type bridge

- What: The chat frontend casts the nullable conversation-title update to the generated string type when clearing a title.
- Why: The OpenAPI document declares version 3.1 but expresses nullability with the OpenAPI 3.0-only `nullable: true` keyword. `@hey-api/openapi-ts` 0.97.3 ignores that keyword for 3.1 input and generates `title?: string`, although the server contract and behavior accept `null`.
- Proper fix: Convert nullable schemas in the OpenAPI contract to valid 3.1 unions that include `null`, regenerate every client, and remove the cast.

## Load test index batching paced to avoid Postgres WAL spike

- **What:** `indexEntries()` in `internal/loadtest/generator/seeder.go` sleeps 50ms between each batch of `POST /v1/conversations/index` requests.
- **Why:** Seeding 2000+ conversations generates 200k+ index writes with up to 5000-char entry text content. Without pacing, the WAL write rate outpaces Postgres checkpoint flushes on a local dev instance, causing `PANIC: could not write to file "pg_wal/xlogtemp.*": No space left on device` and crashing Postgres into recovery mode. The 50ms sleep lets Postgres checkpoint between bursts and prevents the spike.
- **Proper fix:** Run the large-scale seed against a Postgres instance with tuned WAL settings (`max_wal_size`, `wal_buffers`, `shared_buffers`) as noted in issue #598. A compose override for load testing would remove the need for this pacing entirely.
