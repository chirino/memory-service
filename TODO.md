# TODO List

* Figure out how muli-modal content should be handled.
* Expose Metrics
* Support OpenTracing
* Improve the langchain4j memory interface: Switch to the langchain4j MemoryChatStore once https://github.com/langchain4j/langchain4j/pull/4416 is released.
* Brand the project and move it to an org/foundation.

# Hardening Work

* require API_KEY for all api calls.
* validate CreateMessageRequest.userId matches the bearer token principle.
* test grpc resume redirects
* test/find the message size limits of the app.
* validate all api fields
* protect against huge api requests.

# Performance Related

* Getting latest memory of a conversation is likely to be a very frequently accessed operation: cache it.
* are there any http cache/headers that could reduce load against the server?
* Look into partitioning the messages table to improve pref.

# Need Dev Feedback for:

* Can the @RecordConversation bits be moved into Quarkus Langchain4j? https://github.com/quarkiverse/quarkus-langchain4j/issues/2068#issuecomment-3816044002
* Do we need multi-tenancy support?  What would it look like?
* How useful is the current summarize/index feature?
* Ponder how best to kick off/manage async search indexing maybe move this into the admin api?
