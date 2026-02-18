# TODO List

* document index/search apis: provide RAG example
* document admin apis
* Brand the project and move it to an org/foundation.
* Add support for python langchain /w user docs similar to the quarkus/spring support.
* move the ChatEvent json serializer to quarkus
* make the infinispan cache name configurable (see [054-infinispan-cache-name-config.md](docs/enhancements/054-infinispan-cache-name-config.md))
* make all common memory-service config options prefixed with "memory-service."
* make page cursoring more consistent accross the endpoints.

# Hardening Work

* validate all api fields
* review all config key names: keep them consistent and simple.
* test/find the message size limits of the app.
    * use that info to protect against DOS: huge api requests .
* define maxium lengths for all fields.
* Handle syncing Memory entries with more than 1000 messages by splitting into multiple entries (client-side change).

# Performance Related

* are there any http cache/headers that could reduce load against the server?
* Look into partitioning the messages table to improve pref.
    * can we use the sha256 as the ETAG of attachments?

# Need Dev Feedback for:

* Can the @RecordConversation bits be moved into Quarkus Langchain4j? https://github.com/quarkiverse/quarkus-langchain4j/issues/2068#issuecomment-3816044002
   * We have added addional features to the interceptor that might not fit into a generic interceptor: thinks like forking support.
* Do we need MORE multi-tenancy support?  What would it look like? Groups / Orgs?
* Allow runtime configured agents/api-keys?
* How useful is the current index/search feature?
* Do we need to support anonymous user conversations?
