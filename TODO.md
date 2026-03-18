# TODO List

## Potentially Backward Compat Breaking
* should the /sync endpoint take a flatten entries to just a list of content messages?
* a way to support batch processing of old conversations / memories to create/update/reinforce memories
* track memory load counts, as a way to track how important/useful a memory is (see [072-memory-load-counts.md](docs/enhancements/implemented/072-memory-load-counts.md)).

## General
* document index/search apis: provide RAG example (see [042-index-search-docs.md](docs/enhancements/implemented/042-index-search-docs.md))
* support getting getting the clientID from the bearer token.
* Go: Avoid using file buffer for the encryption store.
* get all the python examples working as good as the Java ones.
* improve the memories usecase, add support for it to all the frameworks.
* fix: python request streaming is broken.
* support using github.com/99designs/keyring to store the DEK (local usecases)
* investigate/test/support more async conversation messaging styles.. sending additional user messages to an agent while it's stil streamming a reponse.  Do the messages queue? do they interrupt? 

## Performance Related

* Think about supporting operating against postgresql read replicas.
* Create load tester

## Hardening Work

* add go build tags that can disable features such as UDS, libsql etc..
* protect against large syncs that create new epochs
* limit the size of memory entries.
* update clients to split large contexts into multiple entries to aovid hitting size limits
* make sure we don't load large result sets into server memeory

## Need to discuss

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
