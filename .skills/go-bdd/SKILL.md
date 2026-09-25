---
name: go-bdd
description: Use when writing, editing, or debugging the Go BDD suite, including Cucumber feature files (*.feature) under internal/bdd/testdata/, step definitions or runners in internal/bdd/, or the harness in internal/testutil/cucumber. Covers feature-file style, runner layout, scenario isolation, and reading failures.
---

# Go BDD Suite

Test REST and gRPC behavior with the Go BDD suite (`internal/bdd`) rather than unit tests with mocks. Keep unit tests for internal helpers and infrastructure.

For the commands, build tags, and which runners need `auth_testfixtures`, use the `build-test` skill.

## Layout

- Shared features live in `internal/bdd/testdata/features/` (REST) and `features-grpc/`. Backend-specific features go in sibling `features-<backend>/` directories instead of copying the shared suite.
- Each datastore or config has its own runner, `internal/bdd/cucumber_<backend>*_test.go`, with a matching `TestDB` implementation (`testdb_<backend>.go`).
- Outbox behavior has dedicated runners (`TestFeaturesPgOutbox`, `TestFeaturesSQLiteOutbox`, `TestFeaturesMongoOutbox`). The broad suites keep the default config.

## Feature files are API documentation

A reader should understand the API from the feature file without reading step code.

The first time a scenario uses an API, spell out the raw HTTP call and the JSON:

```gherkin
When I call POST "/v1/conversations/${conversationId}/entries" with body:
"""
{
  "channel": "HISTORY",
  "contentType": "history",
  "content": [{"text": "Hello", "role": "USER"}]
}
"""
Then the response status should be 201
And the response body should be json:
"""
{
  "id": "${response.body.id}",
  "conversationId": "${conversationId}",
  "channel": "history",
  "contentType": "history",
  "content": [{"text": "Hello", "role": "USER"}],
  "createdAt": "${response.body.createdAt}"
}
"""
```

After that, shorthand steps are fine:

```gherkin
Given the conversation has an entry "Hello"
When I list entries for the conversation
Then the response should contain 1 entry
```

Capture values for later steps with `And set "myVar" to the json response field "id"` and use them as `${myVar}`.

## Scenario isolation

Scenarios run concurrently, so each one gets a short suffix (`internal/testutil/cucumber`).

- Canonical users (`alice`, `bob`, ...) and agent client IDs are rewritten to isolated values in request paths, bodies, gRPC prototext, and the `X-Client-ID` header. Responses are rewritten back before assertions.
- When production auth metadata must match a user ID embedded in a request body (for example a `user/<id>` memory namespace), build it from `TestScenario.IsolatedUser` (or `IsolatedClientID`), not a literal name.
- Roles: see `internal/bdd/role_users_test.go`. Use `bob` or `erin` when a scenario must prove isolation between ordinary users.
- SQLite runners start a server and database file per scenario. The serial PostgreSQL and MongoDB runners share one server and run with concurrency 1.
- On MongoDB runs, SQL verification steps are skipped (`MongoTestDB.ExecSQL` returns no rows). Add Mongo-specific assertions when datastore state matters.
- `godog.Table.Rows` includes the header row; skip it in table-driven steps.

## Reading failures

The output is long. Redirect it to a file and search it:

```bash
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' ./internal/bdd -run '^TestFeaturesSQLite$' -count=1 > test.log 2>&1
rg -n "FAIL|ERROR|panic|--- FAIL:" test.log
```
