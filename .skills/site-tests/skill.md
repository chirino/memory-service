---
name: site-tests
description: Use when working on documentation tests (site-tests module). Covers test generation pipeline, fixtures, assertions, and debugging.
autoTrigger:
  - files: ["site-tests/**", "site/src/components/TestScenario.astro", "site/src/components/CurlTest.astro", "site/src/pages/docs/spring/**", "site/src/pages/docs/quarkus/**"]
---

# Site Tests — Documentation Test Framework

## Pipeline
```
MDX docs (CurlTest/TestScenario components)
  → Astro build extracts test data
  → site-tests/target/generated-test-resources/test-scenarios.json
  → TestGenerator.java creates .feature file
  → site-tests/target/generated-test-resources/features/documentation-tests.feature
  → Cucumber runs tests
```

**DO NOT edit** `documentation-tests.feature` directly — it's auto-generated. Edit the MDX source files instead.

## Run Tests
```bash
./mvnw test -pl site-tests -Psite-tests
```
Redirect output to file and grep for errors (output is very long).

## MDX Test Components

**TestScenario** wraps a group of CurlTests for a checkpoint:
```mdx
<TestScenario checkpoint="quarkus/examples/doc-checkpoints/03-with-history">
  <CurlTest steps={`...`}>```bash
  curl ...
  ```</CurlTest>
</TestScenario>
```

**CurlTest** `steps` prop contains Cucumber step definitions:
- `Then the response status should be 200`
- `And the response body should be json:` (strict JSON equality via Jackson `JsonNode.equals()`)
- `And the response should contain "text"` (loose substring check)
- `And the response should match pattern "\\d+"` (regex)

## JSON Assertions

Use `%{response.body.field}` for dynamic values (IDs, timestamps). Use static values for deterministic fields (titles, userIds, accessLevels).

```
And the response body should be json:
"""
{
  "id": "%{response.body.id}",
  "title": "My Static Title",
  "ownerUserId": "bob",
  "createdAt": "%{response.body.createdAt}"
}
"""
```

**Array matching is strict** — the expected JSON must have the exact same number of array elements as the actual response. If other test scenarios create data visible to the current test, include those items in the assertion.

## OpenAI Mock (WireMock)

- Fixtures in `site-tests/openai-mock/fixtures/{quarkus|spring}/{checkpoint-name}/`
- Sequential state machine: `001.json` → `002.json` → ... (matched by scenario state, NOT request content)
- Fixtures are loaded per checkpoint via `DockerSteps.loadFixturesForCheckpoint()`
- WireMock resets between checkpoints

## Shared State Gotcha

All test scenarios share ONE memory-service instance. Conversations created in one scenario are visible in others. Use different conversation IDs across tutorials, or write assertions that tolerate extra data.

## Curl Retry Logic

`CurlSteps.java` retries on 404, 500, 503 (transient startup errors). Max 5 retries, 3s delay.

## Key Files
- `site/src/components/TestScenario.astro` — extracts tests from MDX during Astro build
- `site/src/components/CurlTest.astro` — wraps curl commands with hidden test steps
- `site-tests/src/main/java/.../TestGenerator.java` — generates .feature from JSON
- `site-tests/src/test/java/.../steps/CurlSteps.java` — curl execution + assertions
- `site-tests/src/test/java/.../steps/CheckpointSteps.java` — builds/starts/stops apps
- `site-tests/src/test/java/.../steps/DockerSteps.java` — docker compose + WireMock management
