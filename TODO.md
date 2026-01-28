# TODO List

* hide the concept of conversation groups from the API.
* Batch job to delete conversations / epochs that are older than a retention date.
* Getting latest memory of a converstation is likely to be a very frequently accessed operation: cache it.

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
* Document the spring support
* generate project static site to promote and document usage.
* Brand the project and move it to an org/foundation.
* Look into partioning the messages table to improve pref.

# Need Dev Feedback for:

* Conversations id are UUIDs.. should support any string?
* How much should we trust the agent: Should the agent just tell use the user id or do we distrust it and get it from a bearer token?
* All the class/method names of all the public apis should be reviewed.
* Review/Harden the @RecordConversation impl.
* Do we need multi-tenancy support?  What would it look like?
* Ponder how best to kick off/manage async summerization / search indexing
* How useful is the current summarize/index feature?
* Should the Message type in the api contracts be rename to something like Entry/Event/Posting (since it actually holds messages?)
* Should we start thinking about partioned postgresql tables

# Bug List

* bug: make sure we record partial response in history if connection to LLM fails
