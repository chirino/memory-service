# TODO List

* implement memory compaction/summerization by the agent
* Think about how to deal with memory storage for sub/agent interactions
* add cache control headers
* provide a way to cancel a completion.
* test the mongo store implemenation
* add infinispan implementation
* improve the langchain4j memory interface.
* Implment conversation sharing / multi-user conversations
* Switch to using gRPC as the protocol between the Agent and the memory-service
* Manging the react state of conversation + conversation resumption is complex: provide a headless react component that does it for ui implementors.
* Add multi-tennancy support

# Need Dev Feedback for:

* How much should we trust the agent: Should the agent just tell use the user id or do we distrust it and get it from a bearer token?
* All the class/method names in the client/quarkus-extension could use a ponder
* Review/Harden the @RecordConversation impl.
* Is the current Summary feature useful?
* Ponder how best to kick off/manage async summerization / search indexing
* Figure out how muli-modal content should be handled.

# Bug List

*
