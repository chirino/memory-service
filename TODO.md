# TODO List

* Improve the langchain4j memory interface: Switch to the langchain4j MemoryChatStore once https://github.com/langchain4j/langchain4j/pull/4416 is released.

* Figure out how muli-modal content should be handled.

* Implement conversation sharing / multi-user conversations (some backend is there, need front end using to demo)

* Manging the React state of conversation + conversation resumption is complex: provide a headless React component that does it for ui implementors.
* Review cache control headers
* the api proxy allows getting memory channels by the client.
* validate CreateMessageRequest.userId matches the bearer token principle.
* test grpc resume redirects
* test/find the message size limits of the app.
* validate all api fields
* protect against huge api requests.
* DONE: rename memory-service-client to memory-service-rest-quarkus and reorganize modules under quarkus/examples parents.
* Add a Spring AI support lib and example agent.

# Need Dev Feedback for:

* How much should we trust the agent: Should the agent just tell use the user id or do we distrust it and get it from a bearer token?
* All the class/method names in the client/quarkus-extension could use a ponder
* Review/Harden the @RecordConversation impl.
* Do we need multi-tenancy support?  What would it look like?
* Ponder how best to kick off/manage async summerization / search indexing
* How useful is the current summarize/index feature?

# Bug List

*
