# Workarounds

## Spring REST UDS HTTP/1.1 Forcing

- What: `java/spring/memory-service-rest-spring` uses a custom `UnixDomainSocketClientHttpConnector` and reflectively forces Reactor Netty's outbound request to `HTTP/1.1` for Unix-domain-socket REST calls.
- Why: With direct UDS transport, Reactor Netty was emitting `HTTP/3.0` request versions on the socket, and the Go memory-service listener rejected them with `505 HTTP Version Not Supported`.
- Proper fix: Replace the reflective override with a supported Reactor Netty/Spring WebFlux configuration path that guarantees `HTTP/1.1` over UDS, or move Spring REST UDS to a client stack that exposes first-class Unix-socket + HTTP-version control without reflection.

## Mongo GridFS Concurrent Upload Lock

- What: `internal/plugin/attach/mongostore/MongoAttachmentStore.Store` now takes a mutex before calling the shared Mongo `GridFSBucket` upload path.
- Why: `go.mongodb.org/mongo-driver/v2` races inside `GridFSBucket`'s first-write/index-creation path under concurrent attachment uploads, which caused `task test:go -race` failures and corrupted BDD attachment contents in the Mongo backend.
- Proper fix: Replace the store-side lock with a driver-level fix or a safe one-time GridFS index/bootstrap path that avoids shared mutable `GridFSBucket` state during concurrent uploads. I've reported the issue at https://jira.mongodb.org/browse/GODRIVER-3841 

## PostgreSQL Event Outbox Row Cursor

- What: Enhancement 090's PostgreSQL outbox currently uses the `outbox_events.seq` row sequence as the public replay cursor instead of a logical-decoding commit-order cursor.
- Why: This pass needs durable replay storage and a working cursor contract, but the service does not yet run a PostgreSQL logical-decoding relay. A row sequence is easy to persist and replay through the new `EventOutboxStore` interface, even though it is allocated at insert time rather than commit time.
- Proper fix: Replace the row-sequence cursor with a logical-decoding or equivalent commit-ordered cursor source, and publish/tail from that same cursor space so concurrent PostgreSQL transactions cannot be observed out of commit order.
