# TODO List

* document index/search apis: concepts and spring/quarkus howtos (provide RAG example).
* document admin apis
* Brand the project and move it to an org/foundation.
* Add support for python langchain /w user docs similar to the quarkus/spring support.

# Hardening Work

* require API_KEY for all api calls.
* validate CreateMessageRequest.userId matches the bearer token principle.
* test grpc resume redirects on when a loadbalancer sits in front of the memory-service
* validate all api fields
* review all config key names: keep them consistent and simple.
* test/find the message size limits of the app.
    * use that info to protect against DOS: huge api requests .

# Performance Related

* are there any http cache/headers that could reduce load against the server?
* Look into partitioning the messages table to improve pref.

# Need Dev Feedback for:

* Can the @RecordConversation bits be moved into Quarkus Langchain4j? https://github.com/quarkiverse/quarkus-langchain4j/issues/2068#issuecomment-3816044002
   * We have added addional features to the interceptor that might not fit into a generic interceptor: thinks like forking support.
* Do we need MORE multi-tenancy support?  What would it look like? Groups / Orgs?
* Allow runtime configured agents/api-keys?
* How useful is the current index/search feature?
* Do we need to support anonymous user conversations?
