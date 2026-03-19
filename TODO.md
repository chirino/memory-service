# TODO List

## Potential API changes
* a way to support batch processing of old conversations / memories to create/update/reinforce memories
* track memory load counts, as a way to track how important/useful a memory is (see [072-memory-load-counts.md](docs/enhancements/implemented/072-memory-load-counts.md)).

## General
* Support getting getting the clientID from the bearer token - Delegates more config to KeyCloak
* Go: Avoid using in-memory buffer for the encryption store (see [085-streaming-encryption-for-attachments.md](docs/enhancements/085-streaming-encryption-for-attachments.md)).
* support using github.com/99designs/keyring to store the DEK (local usecases)
* investigate/test/support more async conversation messaging styles.. sending additional user messages to an agent while it's stil streamming a reponse.  Do the messages queue? do they interrupt? 
* Allow the MCP interface to the namespaced memeories via embdded local server.
* MCP should focus more on memories than conversations.
* Provide a way to designate stable vs unstable features/apis.

## Better Demo / Usecases

* use conversation index/search apis to provide RAG example (see [042-index-search-docs.md](docs/enhancements/implemented/042-index-search-docs.md))
* improve the memories usecase, add support for it to all the frameworks.
* get all the python examples working as good as the Java ones. reponse streams seem to have a bigger delay.
* build a demo of an agent extracting user pererences, knowlege about the current project, etc and storing it in long term memories.
* Multi-agent collaboration demo — show two or more agents sharing a conversation with different roles/permissions.
* Claude Code/Codex integration example — an agent that uses memory-service as its persistent memory backend (very meta given the project context).

## Performance Related

* Think about supporting operating against postgresql read replicas.
* Create load tester to assist in locating slow downs better index and query strategies

## Hardening Work

* Protect against large syncs that constantly create new epochs
* Limit the size of memory entries.
* Make sure we can support large contexts like 1m tokens
* update clients to split large contexts into multiple entries to aovid hitting size limits
* make sure we don't load large result sets into server memeory
* Security Audit
* Full review of APIs before we lock them down for long term support.
* Full review of the data schemas before we lock them down for long term support.
* Test coverage evaluation
* Rate limiting / throttling — document a recommended solution for per-user or per-client rate limits on API endpoints.

## Release Work

* Publish Images (memory-service, chat-quarkus)
* Publish memory-service cli tool
* Publish jars to the maven central
* Publish Typescript bits to npm
* Publish Python bits to PyPI 
* Publish version locked site maybe at something like: https://chirino.github.io/memory-service/versions/v0.1.1

## Need to discuss

* Can the @RecordConversation bits be moved into Quarkus Langchain4j? https://github.com/quarkiverse/quarkus-langchain4j/issues/2068#issuecomment-3816044002
   * We have added addional features to the interceptor that might not fit into a generic interceptor: things like forking support.
* Do we need MORE multi-tenancy support?  What would it look like? Groups / Orgs? (see [060-multi-tenancy-groups-orgs.md](docs/enhancements/060-multi-tenancy-groups-orgs.md)).

# Future Directions

* Implement LlamaStack apis, so that the memory-service can be used in a stack.
* Per user storage usage tracking / quotas
* Async context management
* Eval integration
* Tiered memory storage (key short term memories in cache), only migrate to data stores once they are going to be retained in the long term.
* OpenTelemetry tracing — distributed tracing across agents, memory-service, and down stream services.

# Cross Project Work

* move the ChatEvent json serializer to quarkus

# Organizational

* Brand the project and move it to an org/foundation.
