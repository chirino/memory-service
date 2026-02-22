# TODO List

* document index/search apis: provide RAG example (see [042-index-search-docs.md](docs/enhancements/042-index-search-docs.md))
* document admin apis
* Brand the project and move it to an org/foundation.
* Add support for python langchain /w user docs similar to the quarkus/spring support. (see [058-python-langchain-support.md](docs/enhancements/058-python-langchain-support.md))
* move the ChatEvent json serializer to quarkus
* make all common memory-service config options prefixed with "memory-service." (see [057-unified-config-key-naming.md](docs/enhancements/057-unified-config-key-naming.md))
* bug: delete a fork, restore: it does not show up restored.
* encrypted file store (see [063-encrypted-file-store.md](docs/enhancements/063-encrypted-file-store.md))

# Hardening Work

* Handle syncing Memory entries with more than 1000 messages by splitting into multiple entries (client-side change).

# Performance Related

* are there any http cache/headers that could reduce load against the server? (see [055-attachment-cache-headers.md](docs/enhancements/055-attachment-cache-headers.md))
* Look into partitioning the messages table to improve pref. (see [059-entries-table-partitioning.md](docs/enhancements/059-entries-table-partitioning.md))
    * can we use the sha256 as the ETAG of attachments? (see [055-attachment-cache-headers.md](docs/enhancements/055-attachment-cache-headers.md))

# Need Dev Feedback for:

* Can the @RecordConversation bits be moved into Quarkus Langchain4j? https://github.com/quarkiverse/quarkus-langchain4j/issues/2068#issuecomment-3816044002
   * We have added addional features to the interceptor that might not fit into a generic interceptor: thinks like forking support.
* Do we need MORE multi-tenancy support?  What would it look like? Groups / Orgs? (see [060-multi-tenancy-groups-orgs.md](docs/enhancements/060-multi-tenancy-groups-orgs.md))
* Allow runtime configured agents/api-keys?
* How useful is the current index/search feature?
* Do we need to support anonymous user conversations?
