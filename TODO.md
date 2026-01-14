# TODO List

* Switch to the to the langchain4j MemoryChatStore once https://github.com/langchain4j/langchain4j/pull/4416 is released.
* Improve the langchain4j memory interface.

* add infinispan implementation
* provide a way to cancel a completion.
* Figure out how muli-modal content should be handled.

* Support mutliple agents participating in one conversation.
* Implment conversation sharing / multi-user conversations (some backend is there, need front end using to demo)

* Manging the react state of conversation + conversation resumption is complex: provide a headless react component that does it for ui implementors.
* Review cache control headers

# Need Dev Feedback for:

* How much should we trust the agent: Should the agent just tell use the user id or do we distrust it and get it from a bearer token?
* All the class/method names in the client/quarkus-extension could use a ponder
* Review/Harden the @RecordConversation impl.
* Do we need multi-tennancy support?  What would it look like?
* Ponder how best to kick off/manage async summerization / search indexing
* How useful is the current summarize/index feature?

# Bug List

*
