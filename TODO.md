# TODO List

* Figure out how muli-modal content should be handled.
    * this will likely impact our history handling APIS.
* Improve the langchain4j memory interface: Switch to the langchain4j MemoryChatStore once https://github.com/langchain4j/langchain4j/pull/4416 is released.
* document sharing: concepts and spring/quarkus howtos
* document index/search apis: concepts and spring/quarkus howtos (provide RAG example).
* document admin apis
* Brand the project and move it to an org/foundation.
* Add support for python langchain /w user docs similar to the quarkus/spring support.

# Hardening Work

* require API_KEY for all api calls.
* validate CreateMessageRequest.userId matches the bearer token principle.
* test grpc resume redirects on whne a loadbalancer sits in front of the memory-service
* validate all api fields
* review all config key names: keep them consistent and simple.
* test/find the message size limits of the app.
    * use that info to protect against DOS: huge api requests .

# Performance Related

* are there any http cache/headers that could reduce load against the server?
* Look into partitioning the messages table to improve pref.

# Need Dev Feedback for:

* Can the @RecordConversation bits be moved into Quarkus Langchain4j? https://github.com/quarkiverse/quarkus-langchain4j/issues/2068#issuecomment-3816044002
* Do we need more multi-tenancy support?  What would it look like?
* Allow runtime configured agents/api-keys?
* How useful is the current index/search feature?
* Ponder how best to kick off/manage async indexing maybe move this into the admin api?
* Do we need to support anonymous user conversations?
