# TODO List

* document index/search apis: provide RAG example (see [042-index-search-docs.md](docs/enhancements/implemented/042-index-search-docs.md))
* make all common memory-service config options prefixed with "memory-service." (see [057-unified-config-key-naming.md](docs/enhancements/057-unified-config-key-naming.md))
* support getting getting the clientID from the bearer token.
* Add tsx/js support vercel AI api.
* Go: Avoid using file buffer for the encryption store.
* can we use generated server stubs for REST handlers?
* topK in vector search
* a way to support batch processing of old conversations / memories to create/update/reinforce memories
* track memory load counts, as a way to track how important/useful a memory is (see [072-memory-load-counts.md](docs/enhancements/implemented/072-memory-load-counts.md)).
* get all the python examples working as good as the Java ones.
* improve ghe memories usecase, add support for it to all the frameworks.
* fix: In the current contract, forkedAtEntryId is supposed to be required whenever forkedAtConversationId is set. allow it to be unset.
* add includeDeleted=true admin
* fix: python request streaming is broken.
* rename: ResponseResumer to ResponseRecorder
* rename: memory channel to the context channel.
* should the /sync endpoint take a flatten entries to just a list of content messages?
* create reference docs?
* python/ts don't configure the lib via ENV vars.
* support using github.com/99designs/keyring to store the DEK (local usecases)
* add go build tags that can disable features such as UDS, libsql etc..
* make MEMORY_SERVICE_ATTACHMENTS_S3_DIRECT_DOWNLOAD false by default 

# Performance Related

* Think about supporting operating against postgresql read replicas.
* Create load tester

# Hardening Work

* protect against large syncs that create new epochs
* limit the size of memory entries.
* update clients to split large contexts into multiple entries to aovid hitting size limits
* make sure we don't load large result sets into server memeory

# Need Dev Feedback for:

* Can the @RecordConversation bits be moved into Quarkus Langchain4j? https://github.com/quarkiverse/quarkus-langchain4j/issues/2068#issuecomment-3816044002
   * We have added addional features to the interceptor that might not fit into a generic interceptor: things like forking support.
* Do we need MORE multi-tenancy support?  What would it look like? Groups / Orgs? (see [060-multi-tenancy-groups-orgs.md](docs/enhancements/060-multi-tenancy-groups-orgs.md)).

# Future Directions

* Implement LlamaStack apis, so that the memory-service can be used in a stack.
* provide MCP interface to the namespaced memeories via API.  Get inspo from https://github.com/doobidoo/mcp-memory-service/blob/main/src/mcp_memory_service/mcp_server.py
* Make it a solution for local agents:
   * provide go embeddeding APIs (make internal/config/* and internal/cmd/Server a public API)
   * Add sqlite data store - https://github.com/asg017/sqlite-vec
   * provide MCP interface to the namespaced memeories via embdded local server.

# Cross Project Work

* move the ChatEvent json serializer to quarkus

# Organizational

* Brand the project and move it to an org/foundation.
