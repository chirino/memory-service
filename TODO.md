# TODO List

* Getting latest memory of a conversation is likely to be a very frequently accessed operation: cache it.

* Improve the langchain4j memory interface: Switch to the langchain4j MemoryChatStore once https://github.com/langchain4j/langchain4j/pull/4416 is released.
* Figure out how muli-modal content should be handled.
* Implement conversation sharing / multi-user conversations (some backend is there, need front end using to demo)
* require API_KEY for all api calls.
* renaming summerization endpoints to search indexing..

* Review cache control headers
* validate CreateMessageRequest.userId matches the bearer token principle.
* test grpc resume redirects
* test/find the message size limits of the app.
* validate all api fields
* protect against huge api requests.
* Brand the project and move it to an org/foundation.
* Look into partitioning the messages table to improve pref.

# Need Dev Feedback for:

* Conversations id are UUIDs.. should support any string?
* Can the @RecordConversation bits be moved into Quarkus Langchain4j?
* Do we need multi-tenancy support?  What would it look like?
* Ponder how best to kick off/manage async search indexing
* How useful is the current summarize/index feature?
* Should the Message type in the api contracts be renamed to something like Entry/Event/Posting (since it actually holds messages?)

# Bug List

* bug: make sure we record partial response in history if connection to LLM fails
