# Site-Tests Module Facts

**Docs test filtering**: Scenarios are taggable by framework and checkpoint. For Python-only loops use:
```bash
./mvnw -Psite-tests -pl site-tests -Dcucumber.filter.tags=@python surefire:test
```

**MDX `CodeFromFile` gotcha**: Match strings must be unique in the target file. Prefer function-signature anchors or `lines="start-end"` over route strings with `{...}` placeholders to avoid MDX parsing/smart-quote mismatches.

**Docs scenario UUID gotcha**: When adding/replacing tutorial scenario conversation IDs, verify each new UUID is unique across docs pages (`rg -l '<uuid>' site/src/pages` should return one file).

**Sharing user isolation**: `CurlSteps` rewrites JSON payload `"userId"` values (`bob`/`alice`/`charlie`) to scenario users (`<user>-<port>`) so docs can stay readable while parallel test isolation still works.

**Checkpoint 04 forking (Java docs)**: `quarkus` and `spring` checkpoint 04 keep chat handlers effectively unchanged; fork metadata is demonstrated by appending entries directly to the Memory Service API with `forkedAtConversationId`/`forkedAtEntryId` and exposing `listConversationForks` on the proxy resource/controller.

**Env var gotcha**: Curl command interpolation supports `${VAR}` but not shell default syntax like `${VAR:-default}`. Use explicit values or plain `${VAR}` placeholders in testable docs.
