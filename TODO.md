# TODO List

## Potential API changes
* a way to support batch processing of old conversations / memories to create/update/reinforce memories

## General
* support using github.com/99designs/keyring to store the DEK (local usecases)
* Extend queue/interrupt async messaging beyond Quarkus sub-agent tasks to normal agent conversations and other applicable frameworks.
* Expose namespaced memories through the embedded MCP server.
* Focus the MCP tool surface more on memories than conversations.
* Provide a way to designate stable vs unstable features/apis.
* Add reliable webhook/Kafka consumers or connectors on top of the implemented outbox-backed event streams.
* Enhancement 091 Mongo follow-up: implement [091-mongo-outbox-transactions.md](docs/enhancements/091-mongo-outbox-transactions.md) so `MongoStore.InWriteTx` uses `mongo.Session` / `WithTransaction` and Mongo outbox replay uses change-stream resume tokens instead of best-effort ObjectID cursors.
* Define how memory policy changes that alter selected indexed attributes trigger schema or reindex migrations.
* Implement the sub agent flows for all the other frameworks.
* Review whether OIDC user ID extraction (`preferred_username` -> `upn` -> `sub`) uses the correct stable identity.

## Better Demo / Usecases

* Extend the conversation index/search examples into a complete RAG example that feeds retrieved results back into the LLM (see [042-index-search-docs.md](docs/enhancements/implemented/042-index-search-docs.md)).
* improve the memories usecase, add support for it to all the frameworks.
* get all the python examples working as good as the Java ones. reponse streams seem to have a bigger delay.
* build a demo of an agent extracting user pererences, knowlege about the current project, etc and storing it in long term memories.
* Multi-agent collaboration demo — show two or more agents sharing a conversation with different roles/permissions.
* Extend the existing Claude Code/Codex MCP session-notes integration into an example that uses namespaced memories as its persistent memory backend.

## Performance Related

* Think about supporting operating against postgresql read replicas.
* Create load tester to assist in locating slow downs better index and query strategies

## Hardening Work

* Protect against large syncs that constantly create new epochs
* Limit the size of memory entries.
* Make sure we can support large contexts like 1m tokens
* update clients to split large contexts into multiple entries to avoid hitting size limits
* Audit remaining endpoints for unbounded result materialization; common conversation-entry listing paths are already bounded and paginated.
* Full review of APIs before we lock them down for long term support.
* Full review of the data schemas before we lock them down for long term support.
* Test coverage evaluation

## Release Work

* Publish TypeScript bits to npm
* Publish Python bits to PyPI
* Publish versioned documentation at paths such as `/memory-service/docs/v0.1.1/`.

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
