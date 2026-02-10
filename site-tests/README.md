# Documentation Tests

Automated testing framework that validates code examples and curl commands in the documentation, ensuring they remain accurate and functional as the codebase evolves.

## Overview

This testing system:
- ✅ **Prevents stale documentation** - Automatically tests all documented code examples and API calls
- ✅ **Tests checkpoint applications** - Builds and runs example applications from `spring/examples/doc-checkpoints/`
- ✅ **Validates API responses** - Ensures curl commands return expected results
- ✅ **Uses realistic mocks** - Embedded WireMock server provides OpenAI-compatible responses
- ✅ **Runs in CI** - Catches documentation drift before it reaches users

## How It Works

### Architecture

```
MDX Documentation Files
        ↓
   (Astro Build)
        ↓
test-scenarios.json
        ↓
  (TestGenerator)
        ↓
  .feature Files
        ↓
   (Cucumber/JUnit)
        ↓
   Test Results
```

### Build Process

1. **Site Build** (`npm run build` in `site/`)
   - Astro renders MDX files containing `<TestScenario>` components
   - `TestScenario.astro` extracts bash commands and expectations
   - Writes structured test data to `site-tests/target/generated-test-resources/test-scenarios.json`

2. **Test Generation** (`TestGenerator.java`)
   - Reads `test-scenarios.json`
   - Generates Cucumber `.feature` files in `site-tests/target/generated-test-resources/features/`
   - Each scenario gets a unique port to avoid conflicts

3. **Test Execution** (`DocTestRunner.java`)
   - Embedded WireMock server starts on port 8090 (OpenAI mock)
   - Docker Compose starts required services (memory-service, keycloak)
   - Builds checkpoint applications with Maven
   - Starts applications on assigned ports
   - Executes curl commands and validates responses
   - Cleans up all processes (even on failure)

## Adding Tests to Documentation

### Basic Test Scenario

Wrap your code examples in a `<TestScenario>` component and each curl command in a `<CurlTest>` component in your `.mdx` file:

```mdx
import TestScenario from '../../../components/TestScenario.astro';
import CurlTest from '../../../components/CurlTest.astro';

<TestScenario checkpoint="spring/examples/doc-checkpoints/01-basic-agent">

Start the application:

\```bash
./mvnw spring-boot:run
\```

Test with curl:

<CurlTest steps={`
Then the response status should be 200
And the response should contain "I am"
`}>

\```bash
curl -NsSfX POST http://localhost:9090/chat \
  -H "Content-Type: application/json" \
  -d '"Hello, who are you?"'
\```

</CurlTest>

</TestScenario>
```

### Test Scenario Components

#### 1. Checkpoint Reference

```mdx
<TestScenario checkpoint="spring/examples/doc-checkpoints/02-with-memory">
```

- Points to the example application directory
- The test will `mvn clean package` and run this checkpoint

#### 2. CurlTest Component

Each curl command that should be tested is wrapped in a `<CurlTest>` component with Cucumber assertion steps:

```mdx
<CurlTest steps={`
Then the response status should be 200
And the response should contain "success"
`}>

\```bash
curl -NsSfX POST http://localhost:9090/api/endpoint \
  -H "Content-Type: application/json" \
  -d '{"key": "value"}'
\```

</CurlTest>
```

The `steps` prop contains Cucumber step definitions that are:
- **Hidden** from documentation readers (not rendered in the UI)
- **Extracted** by `TestScenario.astro` during the Astro build
- **Included** in the generated `.feature` files for test execution

Available assertion steps:
- `Then the response status should be 200` - HTTP status code check
- `And the response should contain "text"` - String contains check
- `And the response should not contain "text"` - Negative string check
- `And the response should match pattern "\\d+"` - Regex pattern match
- `And the response body should be text:` (with docstring) - Exact text match
- `And the response body should be json:` (with docstring) - JSON structure match
- `And the response should be json with items array` - JSON array check

#### 3. Bash Commands (non-curl)

Bash commands that are not curl (like `./mvnw spring-boot:run` or `function get-token()`) are extracted as setup steps. The `get-token` function definition is automatically recognized and used for `$(get-token)` substitution in subsequent curl commands.

**Shell Quoting**: The test parser handles complex shell quoting including:
- Single quotes: `'text'`
- Escaped quotes: `'it'\''s'` (represents `it's`)
- Double quotes: `"text"`

#### 4. Authentication Tokens

Use `$(get-token)` for Bearer tokens (defaults to bob/bob):

```bash
curl -NsSfX POST http://localhost:9090/api/endpoint \
  -H "Authorization: Bearer $(get-token)" \
  -d '{"data": "value"}'
```

For multi-user scenarios, use `$(get-token username password)`:

```bash
curl -sSfX GET http://localhost:9090/v1/conversations/ID/memberships \
  -H "Authorization: Bearer $(get-token alice alice)" | jq
```

The test framework automatically:
1. Obtains tokens from Keycloak for bob, alice, and charlie at startup
2. Substitutes `$(get-token)` / `$(get-token user pass)` with actual tokens
3. Executes the authenticated request

### Complete Example

```mdx
---
title: "Getting Started"
---
import TestScenario from '../../../components/TestScenario.astro';
import CurlTest from '../../../components/CurlTest.astro';

## Quick Start

<TestScenario checkpoint="spring/examples/doc-checkpoints/02-with-memory">

Start the application:

\```bash
./mvnw spring-boot:run
\```

Define the get-token function:

\```bash
function get-token() {
  curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "client_id=memory-service-client" \
    -d "client_secret=change-me" \
    -d "grant_type=password" \
    -d "username=bob" \
    -d "password=bob" \
    | jq -r '.access_token'
}
\```

Create a conversation:

<CurlTest steps={`
Then the response status should be 200
And the response body should be text:
"""
Hello Hiram! I'm an AI assistant here to help you...
"""
`}>

\```bash
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Hi, I'\''m Hiram, who are you?"'
\```

</CurlTest>

Ask a follow-up question:

<CurlTest steps={`
Then the response status should be 200
And the response should contain "Hiram"
`}>

\```bash
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Who am I?"'
\```

</CurlTest>

</TestScenario>
```

## Running Tests

### Prerequisites

- Docker and Docker Compose
- Maven
- Node.js and npm

### Run All Tests

```bash
./mvnw test -pl site-tests -Ptest-docs
```

This will:
1. Install npm dependencies for the site
2. Build the site (generates test-scenarios.json)
3. Generate Cucumber feature files
4. Start Docker Compose services
5. Run all documentation tests
6. Clean up all resources

### Run from Any Directory

The tests automatically detect the project root, so you can run from anywhere:

```bash
cd site-tests
../mvnw test -Ptest-docs
```

### Development Workflow

When updating documentation:

1. **Edit MDX files** in `site/src/pages/docs/`
2. **Run tests**: `./mvnw test -pl site-tests -Ptest-docs`
3. **Check logs**: Checkpoint output is piped to stdout with `[checkpoint:PORT]` prefixes
4. **Iterate**: Fix any failures and re-run

### Debugging Failed Tests

#### View Application Logs

Checkpoint output is piped to stdout with `[checkpoint:PORT]` prefixes (e.g., `[checkpoint:10090]`). Build output uses `[build]` prefixes.

#### Test Only Specific Checkpoint

Temporarily remove other scenarios from `test-scenarios.json` and re-run.

#### Check Service Health

```bash
# Verify services are running
docker compose ps

# Check OpenAI mock
curl http://localhost:8090/v1/models

# Check memory-service
curl http://localhost:8082/v1/health
```

## Mock OpenAI Configuration

The tests use an embedded WireMock server (wiremock-standalone v3.11.0, running as a Java process on port 8090) to mock OpenAI API responses for deterministic testing. No Docker container is needed for WireMock.

### Configuration Files

Located in `site-tests/openai-mock/mappings/`:

**`models.json`** - Available models:
```json
{
  "request": {
    "method": "GET",
    "urlPath": "/v1/models"
  },
  "response": {
    "jsonBody": {
      "data": [
        {"id": "gpt-4", "object": "model"},
        {"id": "gpt-3.5-turbo", "object": "model"}
      ]
    }
  }
}
```

**`chat-completions.json`** - Chat responses:
```json
{
  "request": {
    "method": "POST",
    "urlPath": "/v1/chat/completions"
  },
  "response": {
    "body": "{\"choices\":[{\"message\":{\"content\":\"I am Claude...\"}}]}",
    "transformers": ["response-template"]
  }
}
```

### Customizing Responses

#### Change Default Response

Edit `site-tests/openai-mock/mappings/chat-completions.json`:

```json
{
  "response": {
    "body": "{...\"content\":\"Your custom response here\"...}"
  }
}
```

#### Add Request-Specific Responses

Create additional mapping files with `bodyPatterns` to match specific inputs:

**`mappings/chat-who-are-you.json`**:
```json
{
  "priority": 1,
  "request": {
    "method": "POST",
    "urlPath": "/v1/chat/completions",
    "bodyPatterns": [
      {
        "matchesJsonPath": "$.messages[?(@.content =~ /.*who are you.*/i)]"
      }
    ]
  },
  "response": {
    "body": "{\"choices\":[{\"message\":{\"content\":\"I am Claude, an AI assistant.\"}}]}"
  }
}
```

Lower `priority` values are matched first. Default mapping should have no priority (defaults to 5).

#### Apply Changes

Mapping files are loaded dynamically via the WireMock admin API per-checkpoint. Changes to mapping files take effect on the next test run (no rebuild needed).

### Response Templates

WireMock supports Handlebars templating for dynamic responses:

```json
{
  "body": "{\"id\":\"chat-{{randomValue length=20}}\",\"model\":\"{{jsonPath request.body '$.model'}}\",\"created\":1707440400}"
}
```

Available helpers:
- `{{randomValue length=20 type='ALPHANUMERIC'}}` - Random string
- `{{jsonPath request.body '$.path'}}` - Extract from request
- Fixed values for deterministic testing

See [WireMock Response Templating](https://wiremock.org/docs/response-templating/) for more options.

## Test Infrastructure

### Key Components

**`DockerSteps.java`**
- Starts embedded WireMock server (OpenAI mock on port 8090)
- Manages Docker Compose lifecycle (memory-service, keycloak)
- Obtains Keycloak authentication tokens (bob, alice, charlie)
- WireMock fixture recording/playback with scenario state machine
- Waits for service readiness

**`CheckpointSteps.java`**
- Builds checkpoint applications with Maven
- Detects Quarkus vs Spring Boot applications and starts accordingly
- Assigns unique ports (10090, 10091, 10092, ...)
- Configures OPENAI_BASE_URL to point to embedded WireMock
- Manages WireMock fixture loading (playback) or real API key (recording) per checkpoint
- Tracks processes for cleanup with JVM shutdown hooks
- Handles shutdown with OS-level kill

**`CurlSteps.java`**
- Parses curl commands to HTTP requests
- Substitutes environment variables (`$(get-token)`)
- Executes requests with HttpClient
- Validates responses against expectations

**`TestGenerator.java`**
- Reads `test-scenarios.json`
- Generates Cucumber `.feature` files
- Assigns ports and replaces in curl commands
- Creates Background steps for setup

### Process Cleanup

The test framework ensures all processes are terminated:

1. **`@After` Hook** - Runs after each scenario
2. **JVM Shutdown Hook** - Catches unexpected exits
3. **OS-Level Kill** - Uses `kill -9` as last resort
4. **Port Release Check** - Waits for ports to be freed

This prevents zombie processes from holding ports between test runs.

## CI/CD Integration

### GitHub Actions

Create `.github/workflows/test-documentation.yml`:

```yaml
name: Test Documentation

on:
  push:
    branches: [main]
  pull_request:
    paths:
      - 'site/src/pages/docs/**'
      - 'spring/examples/doc-checkpoints/**'
      - 'quarkus/examples/doc-checkpoints/**'
      - 'site-tests/**'

jobs:
  test-docs:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-java@v4
        with:
          java-version: '21'
          distribution: 'temurin'

      - uses: actions/setup-node@v4
        with:
          node-version: '20'

      - name: Test Documentation
        run: ./mvnw test -pl site-tests -Ptest-docs

      - name: Upload Logs
        if: failure()
        uses: actions/upload-artifact@v4
        with:
          name: test-logs
          path: site-tests/target/surefire-reports/
```

### Path Filters

The workflow only runs when documentation or checkpoint code changes, not on every commit.

## Troubleshooting

### LogManager Error

**Error**: `Could not load Logmanager "org.jboss.logmanager.LogManager"`

**Solution**: Already fixed in `pom.xml`:
```xml
<systemPropertyVariables>
  <java.util.logging.manager>java.util.logging.LogManager</java.util.logging.manager>
</systemPropertyVariables>
```

### Port Already in Use

**Error**: `Port 9090 was already in use`

**Cause**: Zombie process from previous test run

**Solution**: Kill processes manually:
```bash
lsof -i :9090 -i :9091 | grep java
kill -9 <PID>
```

Or let the test framework handle it - it should auto-cleanup on next run.

### Request Timeout

**Error**: `java.net.http.HttpTimeoutException: request timed out`

**Cause**: Application failed to start or WireMock mock not responding

**Solutions**:
1. Check application logs in stdout (prefixed with `[checkpoint:PORT]`)
2. Verify WireMock is running: `curl http://localhost:8090/__admin/mappings`
3. Check WireMock mappings: `curl http://localhost:8090/v1/models`

### Build Failures

**Error**: `BUILD FAILURE` during Maven build

**Solutions**:
1. Clean build: `./mvnw clean -pl site-tests`
2. Verify checkpoint compiles: `cd spring/examples/doc-checkpoints/01-basic-agent && ../../../mvnw clean package`
3. Check for syntax errors in checkpoint code

### Docker Compose Issues

**Error**: `docker compose` commands fail

**Solutions**:
1. Ensure `OPENAI_API_KEY` is set: `export OPENAI_API_KEY=test-key`
2. Check Docker is running: `docker ps`
3. Restart services: `docker compose down && docker compose up -d`

## WireMock Recording Mode

The test framework supports recording real OpenAI API responses for deterministic playback.

### Record Fixtures

```bash
SITE_TEST_RECORD=true OPENAI_API_KEY=sk-... ./mvnw test -pl site-tests -Ptest-docs
```

This proxies requests to the real OpenAI API and saves responses as numbered fixture files in `site-tests/openai-mock/fixtures/{framework}/{checkpoint-name}/` (e.g., `001.json`, `002.json`).

### Re-record All Fixtures

```bash
SITE_TEST_RECORD=all OPENAI_API_KEY=sk-... ./mvnw test -pl site-tests -Ptest-docs
```

Use `SITE_TEST_RECORD=all` to overwrite existing fixtures.

### Playback Mode (Default)

```bash
./mvnw test -pl site-tests -Ptest-docs
```

Fixture files are loaded as WireMock scenario-based stubs that fire sequentially (1st request gets 1st recorded response, 2nd gets 2nd, etc.). Falls back to default `chat-completions.json` if no fixtures exist for a checkpoint.

## Best Practices

### Documentation Testing Guidelines

1. **Test Critical Paths** - Focus on common user workflows
2. **Keep Tests Simple** - One concept per test scenario
3. **Use Realistic Data** - UUIDs, realistic names, actual patterns users would use
4. **Document Prerequisites** - List required services, configurations
5. **Provide Context** - Explain what each curl command does
6. **Test Incrementally** - Build up complexity across multiple commands

### Expectations Guidelines

1. **Be Specific** - Match on unique strings that indicate success
2. **Avoid Fragile Matches** - Don't match on exact formatting or order
3. **Test Behavior** - Focus on functionality, not exact responses
4. **Consider Mock Limitations** - Mock returns fixed responses, not contextual

### Maintenance

1. **Update Mocks** - Keep OpenAI mock responses realistic
2. **Review Logs** - Check checkpoint logs for warnings
3. **Monitor CI** - Address failures quickly
4. **Update Checkpoints** - Keep example code current

## References

- [WireMock Documentation](https://wiremock.org/)
- [Cucumber Documentation](https://cucumber.io/docs/cucumber/)
- [Astro Components](https://docs.astro.build/en/core-concepts/astro-components/)
- [Memory Service Documentation](../site/src/pages/docs/)

## Support

For issues or questions:
1. Check logs in `site-tests/target/`
2. Review this README
3. Check existing issues on GitHub
4. Create new issue with logs attached
