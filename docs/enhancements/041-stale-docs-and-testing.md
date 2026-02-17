---
status: implemented
---

# Enhancement 041: Stale Documentation Prevention and Testing

> **Status**: Implemented.

**Status**: ✅ Fully Implemented

## Implementation Status

### ✅ Phase 1: MDX Code Components - COMPLETE

1. **CodeFromFile Component** (`site/src/components/CodeFromFile.astro`)
   - Reads code from checkpoint source files at build time
   - Supports three modes: full file, line ranges, pattern matching
   - Generates GitHub source links automatically
   - Build fails if file doesn't exist or match is ambiguous
   - Provides clear error messages for debugging

### ✅ Phase 2: Documentation Test Infrastructure - COMPLETE

1. **TestScenario Component** (`site/src/components/TestScenario.astro`)
   - Extracts bash/curl commands from MDX during build
   - Parses assertions from `<CurlTest steps={...}>` components (prop-based, preferred)
   - Also supports legacy `<Steps>` body-based blocks
   - Writes structured JSON to `site-tests/src/test/resources/test-scenarios.json`
   - Supports deduplication to prevent duplicate test scenarios

2. **CurlTest Component** (`site/src/components/CurlTest.astro`)
   - Wraps a single curl command (bash code block) with Cucumber test assertions
   - `steps` prop contains hidden Cucumber assertion steps
   - Renders the bash block normally for documentation; assertions are hidden
   - TestScenario.astro extracts the hidden assertions during build
   - Replaces the legacy `<Steps>` block pattern with a cleaner per-curl approach

3. **Test Infrastructure** (`site-tests/`)
   - Maven module with Cucumber/JUnit integration
   - `TestGenerator.java` - Converts JSON to Cucumber `.feature` files
   - `CurlParser.java` - Parses curl commands to HttpClient requests
   - `DockerSteps.java` - Docker Compose lifecycle + embedded WireMock server management
   - `CheckpointSteps.java` - Builds and runs checkpoint applications
   - `CurlSteps.java` - Executes curl and validates responses
   - Process cleanup with @After hooks and JVM shutdown hooks
   - Bash function support (`get-token` pattern)

4. **Embedded OpenAI Mock** (WireMock)
   - Embedded `WireMockServer` (wiremock-standalone v3.11.0) running as Java process on port 8090
   - Static mapping files in `site-tests/openai-mock/mappings/` (models.json, chat-completions.json, chat-completions-streaming.json)
   - Deterministic testing with realistic OpenAI-compatible responses
   - Response templating with Handlebars (`globalTemplating(true)`)
   - All mappings loaded via WireMock admin API per-checkpoint (not from filesystem)

5. **HTTP/1.1 Compatibility Fix**
   - All Spring checkpoints configured with `RestClient.Builder` bean
   - Forces HTTP/1.1 to avoid HTTP/2 issues with WireMock
   - Applied to all 5 Spring checkpoint examples

6. **Robust Process Management**
   - Dynamic project root detection (works from any directory)
   - Unique port assignment per scenario (avoids conflicts)
   - Automatic process cleanup (no zombie processes)
   - Port release verification before starting new processes
   - Integration with Maven test lifecycle

### ✅ Phase 3: Test Generation and Validation - COMPLETE

1. **Working Tests** (10 scenarios, all passing)
   - `quarkus/examples/doc-checkpoints/01-basic-agent` - ✅ Passes
   - `quarkus/examples/doc-checkpoints/02-with-memory` - ✅ Passes
   - `quarkus/examples/doc-checkpoints/03-with-history` - ✅ Passes
   - `quarkus/examples/doc-checkpoints/04-advanced-features` - ✅ Passes
   - `quarkus/examples/doc-checkpoints/05-sharing` - ✅ Passes
   - `spring/examples/doc-checkpoints/01-basic-agent` - ✅ Passes
   - `spring/examples/doc-checkpoints/02-with-memory` - ✅ Passes
   - `spring/examples/doc-checkpoints/03-with-history` - ✅ Passes
   - `spring/examples/doc-checkpoints/04-advanced-features` - ✅ Passes
   - `spring/examples/doc-checkpoints/05-sharing` - ✅ Passes
   - All documented curl commands execute successfully
   - Multi-user authentication token substitution working (bob, alice, charlie)
   - Response validation working

2. **CI/CD Workflow** (`.github/workflows/test-documentation.yml`)
   - GitHub Actions workflow created and configured
   - Triggers on changes to docs, checkpoints, or test infrastructure
   - Builds site, generates tests, and runs full test suite
   - Uploads test results as artifacts
   - Fails PR if documented commands don't work

### 📝 Documentation

- ✅ `site-tests/README.md` - Comprehensive guide for using and maintaining tests
- ✅ Enhancement document (this file) fully updated

### ✅ Phase 4: WireMock Recording Proxy for Realistic Fixtures - COMPLETE

1. **Recording Mode** (`SITE_TEST_RECORD=true`)
   - WireMock container starts with `--proxy-all=https://api.openai.com`
   - Requests proxied to real OpenAI API, responses recorded in WireMock journal
   - Per-checkpoint: journal entries saved as numbered fixture files (001.json, 002.json, ...)
   - Fixtures stored in `site-tests/openai-mock/fixtures/{framework}/{checkpoint-name}/`
   - Requires `OPENAI_API_KEY` env var with a valid API key

2. **Playback Mode** (default)
   - Per-checkpoint: fixture files loaded as WireMock scenario-based stubs
   - WireMock Scenarios state machine provides sequential responses (1st request → 1st fixture, 2nd → 2nd, etc.)
   - Falls back to default `chat-completions.json` if no fixtures exist for a checkpoint
   - Scenario state resets between checkpoints

3. **Embedded WireMock Admin API Integration** (`DockerSteps.java`)
   - Embedded `WireMockServer` started on port 8090 (no Docker container needed)
   - HTTP helpers for GET/POST/DELETE against `/__admin/*` endpoints
   - `resetWireMockForCheckpoint()` — clears mappings, journal, and scenario state; re-loads models.json
   - `loadFixturesForCheckpoint(id)` — reads fixture files, POSTs as scenario stubs
   - `saveFixturesFromJournal(id)` — extracts chat completion responses from journal, saves as fixture files
   - `resetScenarios()` — resets state machine to "Started"

4. **Checkpoint Lifecycle Hooks** (`CheckpointSteps.java`)
   - Before starting app: reset WireMock, load fixtures (playback) or configure real API key (recording)
   - Before stopping app: save fixtures from journal (recording) or reset scenarios (playback)

5. **Usage**
   - Record: `SITE_TEST_RECORD=true OPENAI_API_KEY=sk-... ./mvnw test -pl site-tests -Ptest-docs`
   - Playback: `./mvnw test -pl site-tests -Ptest-docs`

### 🚫 Intentionally Not Implemented

1. **"Tested ✓" Badges** - Removed from design (adds visual clutter)
2. **Cucumber HTML Reports** - Removed (use standard surefire test results instead)
3. **Legacy `<Steps>` blocks** - Replaced by `<CurlTest steps={...}>` component which provides per-curl assertions in a cleaner syntax
## Problem Statement

Tutorial documentation in `site/src/pages/docs/` contains code snippets and curl commands that can become stale when:
- Checkpoint example code changes
- APIs evolve
- Dependencies update
- Configuration requirements change

This leads to frustrated users following outdated tutorials. We need automated ways to:
1. **Keep code snippets in sync** with checkpoint source files
2. **Validate tutorial instructions** (curl commands, build steps) actually work

## Goals

1. Replace hardcoded code blocks with references to checkpoint source files
2. Support partial file inclusion (show only relevant sections of a file)
3. Automatically test all curl commands in tutorials
4. Verify each checkpoint builds, runs, and responds correctly
5. Fail builds when docs reference non-existent files or lines
6. Make it obvious when tutorials need updating

## Solution 1: MDX Components for Code Inclusion

### Approach

Create custom MDX components that fetch and display code at build time.

### Implementation

Create `site/src/components/CodeFromFile.astro`:

```astro
---
import { readFileSync } from 'fs';
import { resolve } from 'path';

interface Props {
  file: string;
  lang?: string;
  lines?: string;
  before?: number;
  after?: number;
}

const { file, lang = 'java', lines, before = 0, after = 0 } = Astro.props;

// Get match string from slot content (if provided)
const slotContent = await Astro.slots.render('default');
const matchString = slotContent ? slotContent.trim() : null;

// Resolve relative to project root
const filePath = resolve(process.cwd(), '..', file);

let content: string;
let lineStart = 1;

try {
  const fileContent = readFileSync(filePath, 'utf-8');
  const allLines = fileContent.split('\n');

  if (lines) {
    // Extract specific line range (e.g., "10-20")
    const [start, end] = lines.split('-').map(Number);
    content = allLines.slice(start - 1, end).join('\n');
    lineStart = start;
  } else if (matchString) {
    // Find matching lines - support multi-line matches
    const matchLines = matchString.split('\n');
    let matchIndex = -1;
    let matchCount = 0;

    // Search for the match pattern
    for (let i = 0; i <= allLines.length - matchLines.length; i++) {
      let found = true;
      for (let j = 0; j < matchLines.length; j++) {
        if (!allLines[i + j].includes(matchLines[j].trim())) {
          found = false;
          break;
        }
      }
      if (found) {
        matchCount++;
        if (matchCount === 1) {
          matchIndex = i;
        } else {
          // Found more than one match - this is ambiguous!
          throw new Error(
            `Match string found multiple times in ${file}:\n` +
            `First match at line ${matchIndex + 1}, second match at line ${i + 1}\n` +
            `Match string:\n${matchString}\n\n` +
            `Please make the match string more specific to uniquely identify the code section.`
          );
        }
      }
    }

    if (matchIndex === -1) {
      throw new Error(
        `Match string not found in ${file}:\n${matchString}\n\n` +
        `Please verify the match string exists in the file.`
      );
    }

    // Extract content with before/after context
    const start = Math.max(0, matchIndex - before);
    const end = Math.min(allLines.length, matchIndex + matchLines.length + after);
    content = allLines.slice(start, end).join('\n');
    lineStart = start + 1;
  } else {
    // Return entire file
    content = fileContent;
  }
} catch (error) {
  throw new Error(`Failed to read ${file}: ${error.message}`);
}
---

<div class="code-from-file" data-source={file}>
  <pre><code class={`language-${lang}`}>{content}</code></pre>
  <div class="code-source">
    <a href={`https://github.com/chirino/memory-service/tree/main/${file}`} target="_blank">
      View source: {file} {lineStart > 1 && `(lines ${lineStart}+)`}
    </a>
  </div>
</div>
```

### Example Usage

```mdx
import CodeFromFile from '../../../components/CodeFromFile.astro';

Create a simple REST controller:

<CodeFromFile
  file="spring/examples/doc-checkpoints/01-basic-agent/src/main/java/com/example/demo/ChatController.java"
  lang="java"
/>

Or show just the SecurityConfig bean with 3 lines before and 10 lines after:

<CodeFromFile
  file="spring/examples/doc-checkpoints/02-with-memory/src/main/java/com/example/demo/SecurityConfig.java"
  before={3}
  after={10}
  lang="java"
>@Bean</CodeFromFile>

Or match multiple lines (e.g., a method signature):

<CodeFromFile
  file="spring/examples/doc-checkpoints/03-with-history/src/main/java/com/example/demo/ChatController.java"
  before={2}
  after={15}
  lang="java"
>
@PostMapping("/{conversationId}")
public Flux<String> chat(
</CodeFromFile>
```

**Key features:**
- Match string goes in slot content (between tags), supporting multi-line matches
- Separate `before` and `after` props for fine-grained context control
- Build fails if match is ambiguous (found multiple times in file)
- Build fails if match not found
- Line numbers shown in source link when using match

### Pros

- ✅ **Explicit and clear**: Component usage is obvious in MDX
- ✅ **TypeScript support**: Type-checked props in IDEs
- ✅ **Flexible rendering**: Can add custom UI around code blocks
- ✅ **Linkable sources**: Can add "View on GitHub" links automatically

### Cons

- ❌ More verbose than standard code fences
- ❌ Doesn't work with standard markdown (only MDX)
- ❌ Syntax not as familiar to documentation authors

---

## Solution 2: Tutorial Command Testing with Cucumber

### Approach

Parse documentation files to extract curl commands and checkpoint references, then generate Cucumber tests that:
1. Start memory-service via docker compose
2. Build and run each checkpoint
3. Execute documented curl commands
4. Verify expected responses

### Explicit Test Scenario Markup

Doc authors wrap each curl command in a `<CurlTest>` component with Cucumber assertion steps. Each `<CurlTest>` belongs inside a `<TestScenario>` block that identifies the checkpoint to test.

````mdx
<TestScenario checkpoint="spring/examples/doc-checkpoints/01-basic-agent">

<CurlTest steps={`
Then the response status should be 200
And the response body should be text:
"""
Hello Hiram! I'm an AI language model created by OpenAI...
"""
`}>

```bash
curl -NsSfX POST http://localhost:9090/chat \
  -H "Content-Type: application/json" \
  -d '"Hi, I'\''m Hiram, who are you?"'
```

</CurlTest>

<CurlTest steps={`
Then the response status should be 200
And the response should not contain "Hiram"
`}>

```bash
curl -NsSfX POST http://localhost:9090/chat \
  -H "Content-Type: application/json" \
  -d '"Who am I?"'
```

</CurlTest>

</TestScenario>
````

**How `<CurlTest>` works:**
- The `steps` prop contains Cucumber assertion steps (hidden from docs readers)
- The bash code block is rendered normally for documentation
- `TestScenario.astro` extracts both the bash block and the hidden steps during build
- Each curl command gets its own assertions, making test failures easy to diagnose

**Benefits:**
- ✅ Per-curl assertions — each curl command has its own validation steps
- ✅ Assertions are hidden from documentation readers (not rendered in the UI)
- ✅ Full Cucumber step syntax — supports `response status`, `response should contain`, `response body should be text`, `response body should be json`, regex pattern matching, etc.
- ✅ Cleaner than the legacy `<Steps>` block approach (assertions are co-located with their curl command)

### TestScenario Component Implementation

**Key Insight**: Instead of parsing MDX files from Java, generate test data during the Astro build!

The TestScenario component extracts test data to a JSON file during `npm run build`, which the Java tests then read. This eliminates the need for complex MDX parsing.

Create `site/src/components/TestScenario.astro`:

```astro
---
import { writeFileSync, mkdirSync, existsSync, readFileSync } from 'fs';
import { join } from 'path';

interface Props {
  checkpoint: string;
}

const { checkpoint } = Astro.props;

// During build, extract test data and save to JSON
if (import.meta.env.PROD) {
  try {
    // Get the slot content as HTML
    const html = await Astro.slots.render('default');

    // Extract bash blocks and expectations from the rendered content
    const testData = {
      checkpoint,
      sourceFile: Astro.url.pathname,
      scenarios: extractTestScenarios(html)
    };

    // Append to test scenarios file
    const testDataDir = join(process.cwd(), '..', 'site-tests', 'src', 'test', 'resources');
    const testDataFile = join(testDataDir, 'test-scenarios.json');

    if (!existsSync(testDataDir)) {
      mkdirSync(testDataDir, { recursive: true });
    }

    // Read existing scenarios or create new array
    let allScenarios = [];
    if (existsSync(testDataFile)) {
      allScenarios = JSON.parse(readFileSync(testDataFile, 'utf-8'));
    }

    allScenarios.push(testData);
    writeFileSync(testDataFile, JSON.stringify(allScenarios, null, 2));

  } catch (error) {
    console.warn('Failed to extract test scenario:', error);
  }
}

function extractTestScenarios(html: string) {
  const scenarios = [];

  // Find bash code blocks
  const bashBlockRegex = /<code class="language-bash">(.*?)<\/code>/gs;
  let match;

  while ((match = bashBlockRegex.exec(html)) !== null) {
    const bashCode = match[1]
      .replace(/&lt;/g, '<')
      .replace(/&gt;/g, '>')
      .replace(/&amp;/g, '&')
      .replace(/&#39;/g, "'")
      .replace(/&quot;/g, '"');

    // Look for expectations after this code block
    const expectations = [];
    const expectationRegex = /<strong>Expected<\/strong>:\s*(.+?)(?=<|$)/g;
    let expMatch;

    while ((expMatch = expectationRegex.exec(html.substring(match.index))) !== null) {
      const expectText = expMatch[1].trim();

      if (expectText.includes('should contain')) {
        const contentMatch = expectText.match(/"([^"]+)"/);
        if (contentMatch) {
          expectations.push({ type: 'contains', value: contentMatch[1] });
        }
      } else if (expectText.includes('should NOT contain')) {
        const contentMatch = expectText.match(/"([^"]+)"/);
        if (contentMatch) {
          expectations.push({ type: 'not_contains', value: contentMatch[1] });
        }
      } else if (expectText.includes('status code')) {
        const codeMatch = expectText.match(/(\d{3})/);
        if (codeMatch) {
          expectations.push({ type: 'status_code', value: parseInt(codeMatch[1]) });
        }
      }

      break; // Only get expectation immediately after this bash block
    }

    // Look for optional <Steps> block with custom validation steps
    const customSteps = [];
    const stepsBlockRegex = /<Steps>(.*?)<\/Steps>/gs;
    const stepsMatch = stepsBlockRegex.exec(html);
    if (stepsMatch) {
      // Extract text content from <Steps> block
      const stepsContent = stepsMatch[1]
        .replace(/<[^>]+>/g, '') // Remove HTML tags
        .replace(/&lt;/g, '<')
        .replace(/&gt;/g, '>')
        .replace(/&amp;/g, '&')
        .replace(/&#39;/g, "'")
        .replace(/&quot;/g, '"')
        .trim();

      // Split by lines and filter out empty lines
      const stepLines = stepsContent.split('\n')
        .map(line => line.trim())
        .filter(line => line.length > 0);

      customSteps.push(...stepLines);
    }

    scenarios.push({
      bash: bashCode,
      expectations,
      customSteps
    });
  }

  return scenarios;
}
---

<!-- Just render the content as-is, no additional markup -->
<slot />
```

**How it works:**

1. During `npm run build`, the TestScenario component executes for each instance
2. It extracts bash code blocks, **Expected** lines, and optional `<Steps>` blocks from the rendered HTML
3. Writes structured test data to `site-tests/src/test/resources/test-scenarios.json`
4. Renders the slot content unchanged (no visual changes to the docs)
5. Java tests read this JSON file instead of parsing MDX

**Benefits:**
- ✅ No complex MDX parsing needed in Java
- ✅ Astro already handles MDX rendering
- ✅ Single build command generates both site and test data
- ✅ Test data is clean, structured JSON
- ✅ No visual changes to documentation
- ✅ Simpler to maintain and debug

### Implementation Structure

```
memory-service/
├── site-tests/
│   ├── pom.xml
│   ├── src/
│   │   ├── main/
│   │   │   └── java/
│   │   │       └── io/github/chirino/memoryservice/docstest/
│   │   │           ├── DocParser.java              # Parse MDX files
│   │   │           ├── TestScenarioExtractor.java  # Extract <TestScenario> blocks
│   │   │           ├── CurlParser.java             # Parse curl commands
│   │   │           └── TestGenerator.java          # Generate features
│   │   └── test/
│   │       ├── java/
│   │       │   └── io/github/chirino/memoryservice/docstest/
│   │       │       ├── DocTestRunner.java
│   │       │       └── steps/
│   │       │           ├── CheckpointSteps.java
│   │       │           ├── CurlSteps.java
│   │       │           └── DockerSteps.java
│   │       └── resources/
│   │           └── features/
│   │               ├── spring-getting-started.feature      # Generated
│   │               ├── spring-conversation-history.feature # Generated
│   │               └── quarkus-getting-started.feature     # Generated
```

### Doc Parser Example

```java
public class DocParser {

    public static class CheckpointReference {
        String framework;  // "spring" or "quarkus"
        String checkpoint; // "01-basic-agent"
        String stage;      // "starting" or "ending"
    }

    public static class CurlCommand {
        String command;
        String expectedOutput; // Optional
        int checkpointNumber;
    }

    public List<CurlCommand> extractCurlCommands(Path docFile) {
        String content = Files.readString(docFile);
        List<CurlCommand> commands = new ArrayList<>();

        // Find all ```bash code blocks
        Pattern codeBlockPattern = Pattern.compile(
            "```bash\\n(.*?)```",
            Pattern.DOTALL
        );
        Matcher matcher = codeBlockPattern.matcher(content);

        while (matcher.find()) {
            String block = matcher.group(1);

            // Extract curl commands
            if (block.trim().startsWith("curl ")) {
                CurlCommand cmd = new CurlCommand();
                cmd.command = block.trim();

                // Try to find expected output (next code block might be response)
                // ... parsing logic ...

                commands.add(cmd);
            }
        }

        return commands;
    }

    public CheckpointReference extractCheckpointReference(Path docFile, String section) {
        // Parse checkpoint references like:
        // **Starting checkpoint**: spring/examples/doc-checkpoints/01-basic-agent
        // ...
    }
}
```

### Generated Cucumber Feature Example

```gherkin
# Generated from: site/src/pages/docs/spring/getting-started.mdx
Feature: Spring Getting Started Tutorial

  Background:
    Given the memory-service is running via docker compose

  Scenario: Step 1 - Basic Agent Without Memory
    Given I have checkpoint "spring/examples/doc-checkpoints/01-basic-agent"
    When I build the checkpoint with "mvn clean package"
    Then the build should succeed

    When I start the checkpoint on port 9090
    Then the application should be running

    # From line 92-95 of getting-started.mdx
    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:9090/chat \
        -H "Content-Type: application/json" \
        -d '"Hi, I'\''m Hiram, who are you?"'
      """
    Then the response should contain "I am"

    # From line 97-100 of getting-started.mdx
    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:9090/chat \
        -H "Content-Type: application/json" \
        -d '"Who am I?"'
      """
    # Should NOT remember - no memory yet
    Then the response should not contain "Hiram"

    When I stop the checkpoint

  Scenario: Step 2 - With Memory Service
    Given I have checkpoint "spring/examples/doc-checkpoints/02-with-memory"
    When I build the checkpoint with "mvn clean package"
    Then the build should succeed

    When I start the checkpoint on port 9090
    Then the application should be running

    # Test with memory - should remember context
    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        -d '"Hi, I'\''m Hiram, who are you?"'
      """
    Then the response should contain "I am"

    When I execute curl command:
      """
      curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        -d '"Who am I?"'
      """
    Then the response should contain "Hiram"

    When I stop the checkpoint
```

### Step Definitions Example

```java
public class CheckpointSteps {

    private Process checkpointProcess;
    private String checkpointPath;

    @Given("I have checkpoint {string}")
    public void setCheckpoint(String checkpoint) {
        this.checkpointPath = Paths.get("..", checkpoint).toString();
        assertTrue(Files.exists(Paths.get(checkpointPath)),
            "Checkpoint directory does not exist: " + checkpoint);
    }

    @When("I build the checkpoint with {string}")
    public void buildCheckpoint(String buildCommand) throws Exception {
        ProcessBuilder pb = new ProcessBuilder(buildCommand.split(" "));
        pb.directory(new File(checkpointPath));
        pb.redirectErrorStream(true);

        Process process = pb.start();
        int exitCode = process.waitFor();

        if (exitCode != 0) {
            String output = new String(process.getInputStream().readAllBytes());
            fail("Build failed:\n" + output);
        }
    }

    @When("I start the checkpoint on port {int}")
    public void startCheckpoint(int port) throws Exception {
        ProcessBuilder pb = new ProcessBuilder("./mvnw", "spring-boot:run");
        pb.directory(new File(checkpointPath));
        pb.environment().put("SERVER_PORT", String.valueOf(port));
        pb.environment().put("OPENAI_API_KEY", "test-key");

        checkpointProcess = pb.start();

        // Wait for application to be ready
        waitForPort(port, Duration.ofSeconds(30));
    }

    @When("I stop the checkpoint")
    public void stopCheckpoint() {
        if (checkpointProcess != null) {
            checkpointProcess.destroy();
            try {
                checkpointProcess.waitFor(10, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                checkpointProcess.destroyForcibly();
            }
        }
    }
}
```

```java
public class CurlSteps {

    private String lastResponse;
    private int lastStatusCode;

    @When("I execute curl command:")
    public void executeCurl(String curlCommand) throws Exception {
        // Parse curl command and convert to HttpClient request
        CurlParser parser = new CurlParser(curlCommand);

        HttpClient client = HttpClient.newHttpClient();
        HttpRequest request = parser.toHttpRequest();

        HttpResponse<String> response = client.send(
            request,
            HttpResponse.BodyHandlers.ofString()
        );

        lastResponse = response.body();
        lastStatusCode = response.statusCode();
    }

    @Then("the response should contain {string}")
    public void responseShouldContain(String expected) {
        assertTrue(lastResponse.contains(expected),
            "Expected response to contain '" + expected + "' but got: " + lastResponse);
    }

    @Then("the response should not contain {string}")
    public void responseShouldNotContain(String unexpected) {
        assertFalse(lastResponse.contains(unexpected),
            "Expected response to NOT contain '" + unexpected + "' but got: " + lastResponse);
    }

    @Then("the response status should be {int}")
    public void responseStatusShouldBe(int expectedStatus) {
        assertEquals(expectedStatus, lastStatusCode,
            "Expected status " + expectedStatus + " but got " + lastStatusCode);
    }

    /**
     * Validates JSON response with fixture-style comparison and variable substitution.
     * Supports ${response.body.field} syntax for dynamic values.
     *
     * Example:
     * <pre>
     * And the response body should be json:
     * """
     * {
     *   "id": "${response.body.id}",
     *   "title": "My Title",
     *   "count": 42
     * }
     * """
     * </pre>
     */
    @Then("the response body should be json:")
    public void theResponseBodyShouldBeJson(String expectedJson) {
        // Parse both JSONs
        JsonNode actualNode = null, expectedNode = null;
        String expectedPretty = null, actualPretty = null;

        if (expectedJson != null && !expectedJson.isBlank()) {
            try {
                String rendered = renderTemplate(expectedJson);
                expectedNode = OBJECT_MAPPER.readTree(rendered);
                expectedPretty = OBJECT_MAPPER
                        .writerWithDefaultPrettyPrinter()
                        .writeValueAsString(expectedNode);
            } catch (JsonProcessingException e) {
                throw new AssertionError(
                        "Failed to parse expected JSON: " + e.getMessage() + "\nJSON:\n" + expectedJson,
                        e);
            }
        }

        try {
            actualNode = OBJECT_MAPPER.readTree(lastResponse);
            actualPretty = OBJECT_MAPPER
                    .writerWithDefaultPrettyPrinter()
                    .writeValueAsString(actualNode);
        } catch (JsonProcessingException e) {
            throw new AssertionError(
                    "Failed to parse actual JSON: " + e.getMessage() + "\nJSON:\n" + lastResponse,
                    e);
        }

        // Compare semantically (ignoring field order)
        if (actualNode.equals(expectedNode)) {
            return;
        }

        // Build error message with diff
        StringBuilder errorMessage = new StringBuilder();
        errorMessage.append("JSON response body does not match expected:\n\n");

        if (expectedPretty == null) {
            errorMessage.append("No expected JSON provided. Actual JSON:\n");
            errorMessage.append(actualPretty);
        } else {
            // Generate unified diff
            List<String> expectedLines = Arrays.asList(expectedPretty.split("\n"));
            List<String> actualLines = Arrays.asList(actualPretty.split("\n"));

            Patch<String> patch = DiffUtils.diff(expectedLines, actualLines);
            List<String> unifiedDiff = UnifiedDiffUtils.generateUnifiedDiff(
                    "expected.json", "actual.json", expectedLines, patch, 3);

            errorMessage.append("Unified Diff:\n");
            unifiedDiff.forEach(line -> errorMessage.append(line).append("\n"));
        }
        throw new AssertionError(errorMessage.toString());
    }

    /**
     * Template rendering with variable substitution.
     * Supports ${response.body.field} and ${context.variable} syntax.
     */
    private String renderTemplate(String template) {
        if (template == null || template.isBlank()) {
            return template;
        }

        JsonPath responseJson = JsonPath.from(lastResponse);
        JsonPath contextJson = JsonPath.from(serializeContextVariables());

        Pattern PLACEHOLDER_PATTERN = Pattern.compile("\\$\\{([^}]+)}");
        Matcher matcher = PLACEHOLDER_PATTERN.matcher(template);
        StringBuilder result = new StringBuilder();
        int lastIndex = 0;

        while (matcher.find()) {
            result.append(template, lastIndex, matcher.start());
            String expression = matcher.group(1).trim();
            Object value = resolveExpression(expression, responseJson, contextJson);
            boolean inQuotes = isSurroundedByQuotes(template, matcher.start(), matcher.end());
            result.append(serializeReplacement(value, inQuotes));
            lastIndex = matcher.end();
        }
        result.append(template.substring(lastIndex));
        return result.toString();
    }

    private Object resolveExpression(String expression, JsonPath responseJson, JsonPath contextJson) {
        try {
            if (expression.equals("response.body")) {
                return responseJson.get("$");
            }
            if (expression.startsWith("response.body.")) {
                String path = expression.substring("response.body.".length());
                return responseJson.get(path);
            }
            if (expression.equals("context")) {
                return contextVariables;
            }
            if (expression.startsWith("context.")) {
                String path = expression.substring("context.".length());
                return contextJson.get(path);
            }
            if (contextVariables.containsKey(expression)) {
                return contextVariables.get(expression);
            }
        } catch (Exception e) {
            throw new AssertionError("Invalid expression path '" + expression + "': " + e.getMessage(), e);
        }
        throw new AssertionError(
                "Unknown expression '" + expression + "'. Supported: response.body[.*], context[.*]");
    }

    private boolean isSurroundedByQuotes(String template, int start, int end) {
        int before = start - 1;
        int after = end;
        if (before < 0 || after >= template.length()) {
            return false;
        }
        char beforeChar = template.charAt(before);
        char afterChar = template.charAt(after);
        return (beforeChar == '"' && afterChar == '"') || (beforeChar == '\'' && afterChar == '\'');
    }

    private String serializeReplacement(Object value, boolean inQuotes) {
        if (value instanceof String s && !inQuotes) {
            return s;
        }
        try {
            String json = OBJECT_MAPPER.writeValueAsString(value);
            if (inQuotes && json.length() >= 2 && json.startsWith("\"") && json.endsWith("\"")) {
                return json.substring(1, json.length() - 1);
            }
            return json;
        } catch (JsonProcessingException e) {
            throw new AssertionError("Failed to serialize placeholder value: " + e.getMessage(), e);
        }
    }

    private String serializeContextVariables() {
        try {
            return OBJECT_MAPPER.writeValueAsString(contextVariables);
        } catch (JsonProcessingException e) {
            return "{}";
        }
    }

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();
    private final Map<String, Object> contextVariables = new HashMap<>();
}
```

**Dependencies needed in `pom.xml`:**

```xml
<dependency>
    <groupId>io.rest-assured</groupId>
    <artifactId>json-path</artifactId>
    <version>5.3.0</version>
</dependency>
<dependency>
    <groupId>com.fasterxml.jackson.core</groupId>
    <artifactId>jackson-databind</artifactId>
    <version>2.15.2</version>
</dependency>
<dependency>
    <groupId>io.github.java-diff-utils</groupId>
    <artifactId>java-diff-utils</artifactId>
    <version>4.12</version>
</dependency>
```

### Build Integration

Add to root `pom.xml`:

```xml
<profile>
    <id>test-docs</id>
    <modules>
        <module>site-tests</module>
    </modules>
</profile>
```

Run with:

```bash
# Test all documentation
./mvnw test -Ptest-docs

# Test specific framework
./mvnw test -Ptest-docs -Dtest=SpringDocsTest

# Regenerate feature files from docs
./mvnw process-test-resources -Ptest-docs
```

### Simplified TestScenarioLoader (Reads JSON from Build)

**Much simpler approach**: Instead of parsing MDX, read the JSON file generated during `npm run build`.

```java
package io.github.chirino.memoryservice.docstest;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;

/**
 * Loads test scenarios from JSON file generated by Astro build.
 * No MDX parsing needed!
 */
public class TestScenarioLoader {

    // Data classes that match the JSON structure from Astro build
    public static class TestScenarioData {
        public String checkpoint;
        public String sourceFile;
        public List<ScenarioCommand> scenarios;
    }

    public static class ScenarioCommand {
        public String bash;
        public List<Expectation> expectations;
        public List<String> customSteps;  // Optional custom Cucumber steps from <Steps> block
    }

    public static class Expectation {
        public String type;    // "contains", "not_contains", "status_code"
        public String value;   // The expected value
    }

    /**
     * Load all test scenarios from the JSON file generated during site build
     */
    public List<TestScenarioData> loadScenarios(Path jsonFile) throws Exception {
        ObjectMapper mapper = new ObjectMapper();

        String json = Files.readString(jsonFile);
        TestScenarioData[] scenarios = mapper.readValue(json, TestScenarioData[].class);

        return List.of(scenarios);
    }

    /**
     * Load scenarios from default location
     */
    public List<TestScenarioData> loadScenarios() throws Exception {
        Path jsonFile = Path.of("src/test/resources/test-scenarios.json");
        return loadScenarios(jsonFile);
    }
}
```

### CurlParser Implementation

```java
package io.github.chirino.memoryservice.docstest;

import java.net.URI;
import java.net.http.HttpRequest;
import java.net.http.HttpRequest.BodyPublishers;
import java.time.Duration;
import java.util.HashMap;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Converts curl commands to Java HttpClient requests
 */
public class CurlParser {

    private final String curlCommand;
    private final Map<String, String> environment;

    public CurlParser(String curlCommand) {
        this(curlCommand, System.getenv());
    }

    public CurlParser(String curlCommand, Map<String, String> environment) {
        this.curlCommand = curlCommand;
        this.environment = new HashMap<>(environment);
    }

    public HttpRequest toHttpRequest() throws Exception {
        String normalized = normalizeCurlCommand(curlCommand);

        HttpRequest.Builder builder = HttpRequest.newBuilder();

        // Extract URL
        String url = extractUrl(normalized);
        url = replaceEnvironmentVariables(url);
        builder.uri(URI.create(url));

        // Extract method (default to GET if not specified)
        String method = extractMethod(normalized);

        // Extract headers
        Map<String, String> headers = extractHeaders(normalized);
        for (Map.Entry<String, String> header : headers.entrySet()) {
            String value = replaceEnvironmentVariables(header.getValue());
            builder.header(header.getKey(), value);
        }

        // Extract body
        String body = extractBody(normalized);
        if (body != null) {
            body = replaceEnvironmentVariables(body);
            builder.method(method, BodyPublishers.ofString(body));
        } else {
            builder.method(method, BodyPublishers.noBody());
        }

        // Set timeout
        builder.timeout(Duration.ofSeconds(30));

        return builder.build();
    }

    private String normalizeCurlCommand(String curl) {
        // Remove curl prefix
        String normalized = curl.replaceFirst("^curl\\s+", "");

        // Join line continuations
        normalized = normalized.replaceAll("\\\\\\s*\\n\\s*", " ");

        // Normalize whitespace
        normalized = normalized.replaceAll("\\s+", " ");

        return normalized.trim();
    }

    private String extractUrl(String curl) {
        // URL is typically the first argument that starts with http
        Pattern pattern = Pattern.compile("(https?://[^\\s\"']+)");
        Matcher matcher = pattern.matcher(curl);

        if (matcher.find()) {
            return matcher.group(1);
        }

        throw new IllegalArgumentException("No URL found in curl command: " + curl);
    }

    private String extractMethod(String curl) {
        // Look for -X METHOD or --request METHOD
        Pattern pattern = Pattern.compile("-X\\s*([A-Z]+)|--request\\s+([A-Z]+)");
        Matcher matcher = pattern.matcher(curl);

        if (matcher.find()) {
            String method = matcher.group(1);
            if (method == null) {
                method = matcher.group(2);
            }
            return method;
        }

        // Default to POST if there's a body, otherwise GET
        if (curl.contains("-d ") || curl.contains("--data")) {
            return "POST";
        }

        return "GET";
    }

    private Map<String, String> extractHeaders(String curl) {
        Map<String, String> headers = new HashMap<>();

        // Match -H "Header: Value" or --header "Header: Value"
        Pattern pattern = Pattern.compile("(?:-H|--header)\\s+['\"]([^:]+):\\s*([^'\"]+)['\"]");
        Matcher matcher = pattern.matcher(curl);

        while (matcher.find()) {
            String headerName = matcher.group(1).trim();
            String headerValue = matcher.group(2).trim();
            headers.put(headerName, headerValue);
        }

        return headers;
    }

    private String extractBody(String curl) {
        // Match -d "body" or --data "body"
        Pattern pattern = Pattern.compile("(?:-d|--data)\\s+['\"]([^'\"]+)['\"]");
        Matcher matcher = pattern.matcher(curl);

        if (matcher.find()) {
            String body = matcher.group(1);
            // Unescape quotes
            body = body.replace("\\'", "'");
            body = body.replace("\\\"", "\"");
            return body;
        }

        return null;
    }

    private String replaceEnvironmentVariables(String text) {
        // Replace ${VAR_NAME} and $(command) patterns
        Pattern envPattern = Pattern.compile("\\$\\{([^}]+)\\}");
        Matcher matcher = envPattern.matcher(text);

        StringBuffer result = new StringBuffer();
        while (matcher.find()) {
            String varName = matcher.group(1);
            String value = environment.getOrDefault(varName, "");
            matcher.appendReplacement(result, Matcher.quoteReplacement(value));
        }
        matcher.appendTail(result);

        // Handle $(get-token) style command substitutions
        Pattern cmdPattern = Pattern.compile("\\$\\(([^)]+)\\)");
        matcher = cmdPattern.matcher(result.toString());

        result = new StringBuffer();
        while (matcher.find()) {
            String command = matcher.group(1);
            // Command substitutions should be in environment
            String value = environment.getOrDefault("CMD_" + command, "");
            matcher.appendReplacement(result, Matcher.quoteReplacement(value));
        }
        matcher.appendTail(result);

        return result.toString();
    }
}
```

### TestGenerator Implementation (Simplified)

Now the generator just reads structured JSON instead of parsing MDX!

```java
package io.github.chirino.memoryservice.docstest;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

/**
 * Generates Cucumber feature files from JSON test data generated by Astro build.
 * Much simpler - no MDX parsing needed!
 */
public class TestGenerator {

    public void generateFeatureFile(
        List<TestScenarioLoader.TestScenarioData> scenarios,
        Path outputFeatureFile
    ) throws IOException {

        if (scenarios.isEmpty()) {
            return;
        }

        StringBuilder feature = new StringBuilder();

        // Feature header - derive from first scenario's source file
        String featureName = deriveFeatureName(scenarios.get(0).sourceFile);
        feature.append("# Generated from test-scenarios.json (built from MDX)\n");
        feature.append("# DO NOT EDIT: This file is auto-generated\n");
        feature.append("Feature: ").append(featureName).append("\n\n");

        // Background
        feature.append("  Background:\n");
        feature.append("    Given the memory-service is running via docker compose\n");
        feature.append("    And I set up authentication tokens\n\n");

        // Generate scenario for each test scenario
        int scenarioNum = 1;
        for (TestScenarioLoader.TestScenarioData scenario : scenarios) {
            generateScenario(feature, scenario, scenarioNum++);
        }

        // Write to file
        Files.createDirectories(outputFeatureFile.getParent());
        Files.writeString(outputFeatureFile, feature.toString());

        System.out.println("Generated: " + outputFeatureFile);
    }

    private void generateScenario(
        StringBuilder feature,
        TestScenarioLoader.TestScenarioData scenario,
        int scenarioNum
    ) {
        // Scenario header
        String checkpointName = scenario.checkpoint.substring(scenario.checkpoint.lastIndexOf('/') + 1);
        feature.append("  Scenario: Test ").append(checkpointName).append("\n");
        feature.append("    # From ").append(scenario.sourceFile).append("\n");

        // Set up checkpoint
        feature.append("    Given I have checkpoint \"")
               .append(scenario.checkpoint)
               .append("\"\n");

        // Build and start
        feature.append("    When I build the checkpoint\n");
        feature.append("    Then the build should succeed\n\n");
        feature.append("    When I start the checkpoint on port 9090\n");
        feature.append("    Then the application should be running\n\n");

        // Execute each command
        for (TestScenarioLoader.ScenarioCommand command : scenario.scenarios) {
            generateCommand(feature, command);
        }

        // Stop checkpoint
        feature.append("    When I stop the checkpoint\n\n");
    }

    private void generateCommand(
        StringBuilder feature,
        TestScenarioLoader.ScenarioCommand command
    ) {
        // Execute bash command
        feature.append("    When I execute curl command:\n");
        feature.append("      \"\"\"\n");

        for (String line : command.bash.split("\n")) {
            feature.append("      ").append(line.trim()).append("\n");
        }

        feature.append("      \"\"\"\n");

        // Add simple expectations (from **Expected**: lines)
        for (TestScenarioLoader.Expectation expectation : command.expectations) {
            switch (expectation.type) {
                case "contains":
                    feature.append("    Then the response should contain \"")
                           .append(escapeQuotes(expectation.value))
                           .append("\"\n");
                    break;

                case "not_contains":
                    feature.append("    Then the response should not contain \"")
                           .append(escapeQuotes(expectation.value))
                           .append("\"\n");
                    break;

                case "status_code":
                    feature.append("    Then the response status should be ")
                           .append(expectation.value)
                           .append("\n");
                    break;
            }
        }

        // Add custom steps (from <Steps> block)
        if (command.customSteps != null && !command.customSteps.isEmpty()) {
            for (String step : command.customSteps) {
                // Custom steps are already properly formatted Cucumber steps
                // Just need to add proper indentation
                if (step.trim().isEmpty()) {
                    continue;
                }

                // Handle multi-line steps (like JSON fixtures with """)
                if (step.contains("\"\"\"")) {
                    // This is a doc string - output as-is with indentation
                    feature.append("    ").append(step).append("\n");
                } else if (!step.startsWith("And") && !step.startsWith("Then") && !step.startsWith("Given") && !step.startsWith("When")) {
                    // This is a continuation line (like JSON content)
                    feature.append("    ").append(step).append("\n");
                } else {
                    // This is a Cucumber step
                    feature.append("    ").append(step).append("\n");
                }
            }
        }

        feature.append("\n");
    }

    private String deriveFeatureName(String sourceFile) {
        // Extract filename from path like "/docs/spring/getting-started"
        String filename = sourceFile.substring(sourceFile.lastIndexOf('/') + 1);

        // Convert kebab-case to Title Case
        String[] parts = filename.split("-");
        StringBuilder name = new StringBuilder();

        for (String part : parts) {
            if (name.length() > 0) name.append(" ");
            name.append(Character.toUpperCase(part.charAt(0)))
                .append(part.substring(1));
        }

        return name.toString() + " Tutorial";
    }

    private String escapeQuotes(String text) {
        return text.replace("\"", "\\\"");
    }
}
```

### DockerSteps Implementation

```java
package io.github.chirino.memoryservice.docstest.steps;

import io.cucumber.java.After;
import io.cucumber.java.Before;
import io.cucumber.java.en.Given;

import java.io.File;
import java.io.IOException;
import java.net.Socket;
import java.time.Duration;
import java.time.Instant;
import java.util.concurrent.TimeUnit;

public class DockerSteps {

    private Process dockerComposeProcess;
    private static boolean dockerAlreadyRunning = false;

    @Before
    public void setup() {
        // Only start docker compose once for all scenarios
        if (!dockerAlreadyRunning) {
            startDockerCompose();
            dockerAlreadyRunning = true;
        }
    }

    @Given("the memory-service is running via docker compose")
    public void memoryServiceIsRunning() {
        // Verify memory-service is responding
        waitForService("localhost", 8080, Duration.ofSeconds(60));
    }

    @Given("I set up authentication tokens")
    public void setupAuthTokens() {
        // Get auth token from Keycloak and store in environment
        String token = getAuthToken();
        System.setProperty("AUTH_TOKEN", token);
    }

    private void startDockerCompose() {
        try {
            ProcessBuilder pb = new ProcessBuilder(
                "docker", "compose", "up", "-d"
            );
            pb.directory(new File(".."));  // Run from project root
            pb.inheritIO();

            Process process = pb.start();
            int exitCode = process.waitFor();

            if (exitCode != 0) {
                throw new RuntimeException("Docker compose failed to start");
            }

            // Wait for services to be ready
            System.out.println("Waiting for memory-service to be ready...");
            waitForService("localhost", 8080, Duration.ofSeconds(120));

            System.out.println("Waiting for Keycloak to be ready...");
            waitForService("localhost", 8081, Duration.ofSeconds(120));

        } catch (Exception e) {
            throw new RuntimeException("Failed to start docker compose", e);
        }
    }

    private void waitForService(String host, int port, Duration timeout) {
        Instant deadline = Instant.now().plus(timeout);

        while (Instant.now().isBefore(deadline)) {
            try (Socket socket = new Socket(host, port)) {
                // Connection successful
                return;
            } catch (IOException e) {
                // Not ready yet, sleep and retry
                try {
                    Thread.sleep(1000);
                } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    throw new RuntimeException("Interrupted while waiting for service", ie);
                }
            }
        }

        throw new RuntimeException(
            String.format("Service %s:%d did not become ready within %s",
                host, port, timeout)
        );
    }

    private String getAuthToken() {
        try {
            ProcessBuilder pb = new ProcessBuilder(
                "curl", "-sSfX", "POST",
                "http://localhost:8081/realms/memory-service/protocol/openid-connect/token",
                "-H", "Content-Type: application/x-www-form-urlencoded",
                "-d", "client_id=memory-service-client",
                "-d", "client_secret=change-me",
                "-d", "grant_type=password",
                "-d", "username=bob",
                "-d", "password=bob"
            );

            pb.redirectErrorStream(true);
            Process process = pb.start();

            String output = new String(process.getInputStream().readAllBytes());
            int exitCode = process.waitFor();

            if (exitCode != 0) {
                throw new RuntimeException("Failed to get auth token: " + output);
            }

            // Parse JSON response to extract access_token
            // Simple regex extraction (production code should use Jackson)
            java.util.regex.Pattern pattern = java.util.regex.Pattern.compile(
                "\"access_token\"\\s*:\\s*\"([^\"]+)\""
            );
            java.util.regex.Matcher matcher = pattern.matcher(output);

            if (matcher.find()) {
                return matcher.group(1);
            }

            throw new RuntimeException("Could not parse access_token from: " + output);

        } catch (Exception e) {
            throw new RuntimeException("Failed to get auth token", e);
        }
    }

    @After
    public void cleanup() {
        // Keep docker compose running between scenarios for speed
        // Only stop at JVM shutdown
        Runtime.getRuntime().addShutdownHook(new Thread(this::stopDockerCompose));
    }

    private void stopDockerCompose() {
        try {
            ProcessBuilder pb = new ProcessBuilder(
                "docker", "compose", "down"
            );
            pb.directory(new File(".."));
            pb.inheritIO();

            Process process = pb.start();
            process.waitFor(30, TimeUnit.SECONDS);

        } catch (Exception e) {
            System.err.println("Warning: Failed to stop docker compose: " + e.getMessage());
        }
    }
}
```

### Enhanced CurlSteps with Bash Function Support

```java
@When("I define bash function:")
public void defineBashFunction(String functionBody) {
    // Store function in a map for later execution
    bashFunctions.put(extractFunctionName(functionBody), functionBody);
}

@When("I execute curl command:")
public void executeCurl(String curlCommand) throws Exception {
    // Replace bash function calls before executing
    String processedCommand = replaceBashFunctions(curlCommand);

    // Now parse and execute as before
    CurlParser parser = new CurlParser(processedCommand, buildEnvironment());
    // ... rest of execution
}

private String replaceBashFunctions(String command) {
    // Replace $(function-name) with the function's output
    Pattern pattern = Pattern.compile("\\$\\(([\\w-]+)\\)");
    Matcher matcher = pattern.matcher(command);

    StringBuffer result = new StringBuffer();
    while (matcher.find()) {
        String functionName = matcher.group(1);

        if (bashFunctions.containsKey(functionName)) {
            String output = executeBashFunction(bashFunctions.get(functionName));
            matcher.appendReplacement(result, Matcher.quoteReplacement(output));
        }
    }
    matcher.appendTail(result);

    return result.toString();
}

private String executeBashFunction(String functionBody) {
    try {
        // Execute the bash function and capture output
        ProcessBuilder pb = new ProcessBuilder("bash", "-c", functionBody);
        pb.environment().putAll(buildEnvironment());

        Process process = pb.start();
        String output = new String(process.getInputStream().readAllBytes()).trim();
        int exitCode = process.waitFor();

        if (exitCode != 0) {
            throw new RuntimeException("Bash function failed: " + output);
        }

        return output;

    } catch (Exception e) {
        throw new RuntimeException("Failed to execute bash function", e);
    }
}
```

### Maven POM for site-tests Module

```xml
<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0
                             http://maven.apache.org/xsd/maven-4.0.0.xsd">
    <modelVersion>4.0.0</modelVersion>

    <parent>
        <groupId>io.github.chirino</groupId>
        <artifactId>memory-service-parent</artifactId>
        <version>999-SNAPSHOT</version>
    </parent>

    <artifactId>site-tests</artifactId>
    <name>Memory Service :: Documentation Tests</name>

    <dependencies>
        <!-- Cucumber -->
        <dependency>
            <groupId>io.cucumber</groupId>
            <artifactId>cucumber-java</artifactId>
            <scope>test</scope>
        </dependency>
        <dependency>
            <groupId>io.cucumber</groupId>
            <artifactId>cucumber-junit-platform-engine</artifactId>
            <scope>test</scope>
        </dependency>

        <!-- JUnit 5 -->
        <dependency>
            <groupId>org.junit.platform</groupId>
            <artifactId>junit-platform-suite</artifactId>
            <scope>test</scope>
        </dependency>
        <dependency>
            <groupId>org.junit.jupiter</groupId>
            <artifactId>junit-jupiter</artifactId>
            <scope>test</scope>
        </dependency>

        <!-- For JSON parsing (auth tokens) -->
        <dependency>
            <groupId>com.fasterxml.jackson.core</groupId>
            <artifactId>jackson-databind</artifactId>
        </dependency>
    </dependencies>

    <build>
        <plugins>
            <!-- Step 1: Build the documentation site to generate test-scenarios.json -->
            <plugin>
                <groupId>org.codehaus.mojo</groupId>
                <artifactId>exec-maven-plugin</artifactId>
                <version>3.1.0</version>
                <executions>
                    <!-- Install npm dependencies -->
                    <execution>
                        <id>npm-install</id>
                        <phase>generate-test-resources</phase>
                        <goals>
                            <goal>exec</goal>
                        </goals>
                        <configuration>
                            <executable>npm</executable>
                            <arguments>
                                <argument>ci</argument>
                            </arguments>
                            <workingDirectory>../site</workingDirectory>
                        </configuration>
                    </execution>

                    <!-- Build the site (generates test-scenarios.json) -->
                    <execution>
                        <id>npm-build</id>
                        <phase>generate-test-resources</phase>
                        <goals>
                            <goal>exec</goal>
                        </goals>
                        <configuration>
                            <executable>npm</executable>
                            <arguments>
                                <argument>run</argument>
                                <argument>build</argument>
                            </arguments>
                            <workingDirectory>../site</workingDirectory>
                        </configuration>
                    </execution>

                    <!-- Generate Cucumber feature files from test-scenarios.json -->
                    <execution>
                        <id>generate-features</id>
                        <phase>generate-test-resources</phase>
                        <goals>
                            <goal>java</goal>
                        </goals>
                        <configuration>
                            <mainClass>io.github.chirino.memoryservice.docstest.TestGeneratorMain</mainClass>
                            <arguments>
                                <argument>../site/src/pages/docs</argument>
                                <argument>src/test/resources/features</argument>
                            </arguments>
                        </configuration>
                    </execution>
                </executions>
            </plugin>

            <!-- Run tests -->
            <plugin>
                <groupId>org.apache.maven.plugins</groupId>
                <artifactId>maven-surefire-plugin</artifactId>
                <configuration>
                    <includes>
                        <include>**/*Test.java</include>
                        <include>**/DocTestRunner.java</include>
                    </includes>
                    <systemPropertyVariables>
                        <cucumber.publish.quiet>true</cucumber.publish.quiet>
                    </systemPropertyVariables>
                </configuration>
            </plugin>
        </plugins>
    </build>
</project>
```

### Build Workflow

**Single command does everything**: `./mvnw test -Ptest-docs`

The workflow runs automatically in this order:

1. **Site Build** (during `generate-test-resources` phase):
   - Maven runs `npm ci` in site/ directory
   - Maven runs `npm run build` in site/ directory
   - Astro renders MDX files
   - `TestScenario` components extract test data
   - Writes `site-tests/src/test/resources/test-scenarios.json`

2. **Test Generation** (during `generate-test-resources` phase):
   - `TestScenarioLoader` reads JSON file
   - `TestGenerator` creates `.feature` files
   - Feature files written to `site-tests/src/test/resources/features/`

3. **Test Execution** (during `test` phase):
   - Cucumber runs generated features
   - Docker compose starts memory-service
   - Checkpoints are built and tested
   - Results in Cucumber HTML reports

**Key benefits**:
- ✅ No MDX parsing in Java - Astro handles it during build
- ✅ Single Maven command handles everything
- ✅ Ensures test-scenarios.json is always up-to-date
- ✅ No manual npm commands needed

### CI/CD Integration

`.github/workflows/test-documentation.yml`:

```yaml
name: Test Documentation

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]
    paths:
      - 'site/src/pages/docs/**'
      - 'spring/examples/doc-checkpoints/**'
      - 'quarkus/examples/doc-checkpoints/**'
      - 'site-tests/**'

jobs:
  test-docs:
    runs-on: ubuntu-latest
    timeout-minutes: 60

    steps:
      - uses: actions/checkout@v3

      - name: Set up JDK 21
        uses: actions/setup-java@v3
        with:
          java-version: '21'
          distribution: 'temurin'

      - name: Cache Maven packages
        uses: actions/cache@v3
        with:
          path: ~/.m2
          key: ${{ runner.os }}-m2-${{ hashFiles('**/pom.xml') }}

      - name: Set up Node.js
        uses: actions/setup-node@v3
        with:
          node-version: '20'

      - name: Build memory-service
        run: ./mvnw clean install -DskipTests

      - name: Start Docker services
        run: docker compose up -d

      - name: Wait for services
        run: |
          timeout 120 bash -c 'until curl -sf http://localhost:8080/health; do sleep 2; done'
          timeout 120 bash -c 'until curl -sf http://localhost:8081; do sleep 2; done'

      - name: Run documentation tests (builds site and runs tests)
        run: ./mvnw test -Ptest-docs

      - name: Upload test reports
        if: always()
        uses: actions/upload-artifact@v3
        with:
          name: cucumber-reports
          path: site-tests/target/cucumber-reports/

      - name: Stop Docker services
        if: always()
        run: docker compose down
```

### Mock LLM for Testing

**LocalAI is included in compose.yaml** and provides deterministic responses for testing!

[LocalAI](https://github.com/mudler/LocalAI) is an OpenAI-compatible API included in our standard `compose.yaml`:

```yaml
# Already configured in compose.yaml
localai:
  image: quay.io/go-skynet/local-ai:latest
  environment:
    MODELS_PATH: /models
    DEBUG: "false"
    THREADS: 4
  volumes:
    - ./common/localai/models:/models:ro
  ports:
    - "8090:8080"
  healthcheck:
    test: ["CMD-SHELL", "curl -sf http://localhost:8080/readyz"]
    interval: 10s
    timeout: 5s
    retries: 10
    start_period: 20s
```

Model configurations are in `common/localai/models/`:

```yaml
# common/localai/models/gpt-4.yaml
name: gpt-4
backend: echo
parameters:
  echo_message: "I am an AI assistant. I'm here to help you with your questions."
context_size: 8192
f16: true
threads: 4
```

**LocalAI is available for testing**, but not used by default. To use it in checkpoint tests:

**For external access** (from host machine):
```bash
export OPENAI_BASE_URL=http://localhost:8090/v1
export OPENAI_API_KEY=not-needed  # Any value works
```

**For docker compose services** (from within the docker network):
```bash
export OPENAI_BASE_URL=http://localai:8080/v1
export OPENAI_API_KEY=not-needed
```

The demo-chat service still requires a real OpenAI API key by default. Tests will override this to use LocalAI.

**Benefits of using LocalAI:**
- ✅ **Deterministic**: Same responses every time
- ✅ **Fast**: No network calls, instant responses
- ✅ **Free**: No API costs
- ✅ **Offline**: Works without internet
- ✅ **Integrated**: Starts automatically with `docker compose up`

**Customizing responses for specific test scenarios:**

Edit the model configuration files in `common/localai/models/` to change responses:

```yaml
# common/localai/models/gpt-4.yaml
name: gpt-4
backend: echo
parameters:
  # Change this message for different test scenarios
  echo_message: "Your custom test response here"
```

You can also create additional model files for specific test needs.

### Key Insight: Tests Live in the Docs

**The crucial benefit**: By embedding test scenarios directly in the documentation using `<TestScenario>` blocks, we ensure the documented curl commands and the tested commands are **literally the same text**. This eliminates the synchronization problem entirely:

- When docs are updated, tests update automatically
- What users see is exactly what gets tested
- No separate test suite to maintain
- Single source of truth for tutorial instructions

### Pros

- ✅ **Perfect synchronization**: Documented commands ARE the tested commands
- ✅ **Automated validation**: Every marked curl command is tested
- ✅ **End-to-end testing**: Tests full checkpoint lifecycle (build, run, verify)
- ✅ **Documentation as specification**: Docs become executable tests
- ✅ **Catches regressions**: CI fails if tutorials break
- ✅ **Simple for authors**: Just wrap testable sections in `<TestScenario>` tags
- ✅ **Framework familiar**: Uses existing Cucumber infrastructure
- ✅ **CI integrated**: Runs automatically on doc and checkpoint changes
- ✅ **No drift**: Impossible for docs and tests to get out of sync

### Cons

- ❌ **Slow execution**: Building/running checkpoints takes time (30-45 min full suite)
  - Mitigation: Only run on doc/checkpoint changes, not every commit
- ❌ **Resource intensive**: Multiple services (memory-service, Keycloak) running during tests
  - Mitigation: Reuse docker compose across scenarios
- ❌ **LLM non-determinism**: Real AI responses vary
  - Mitigation: Use mock LLM in CI, only test that endpoints respond
- ❌ **Extra markup in docs**: Authors must add `<TestScenario>` tags
  - Mitigation: Tags render invisibly or as helpful "tested" badges

---

## Complete Example: Docs with Code Inclusion and Testing

Here's what a tutorial page would look like using both solutions together:

**File: `site/src/pages/docs/spring/getting-started.mdx`**

````mdx
---
layout: ../../../layouts/DocsLayout.astro
title: Spring Getting Started
---
import CodeFromFile from '../../../components/CodeFromFile.astro';
import TestScenario from '../../../components/TestScenario.astro';

## Step 1: Create a Simple Agent

**Starting checkpoint**: [spring/examples/doc-checkpoints/01-basic-agent](...)

First, create a REST controller:

<CodeFromFile
  file="spring/examples/doc-checkpoints/01-basic-agent/src/main/java/com/example/demo/ChatController.java"
  lang="java"
/>

Configure your application:

<CodeFromFile
  file="spring/examples/doc-checkpoints/01-basic-agent/src/main/resources/application.properties"
  lang="properties"
/>

Now let's test it:

<TestScenario checkpoint="spring/examples/doc-checkpoints/01-basic-agent">

```bash
export OPENAI_API_KEY=your-api-key
./mvnw spring-boot:run
```

Try chatting with the agent:

```bash
curl -NsSfX POST http://localhost:9090/chat \
  -H "Content-Type: application/json" \
  -d '"Hi, I'\''m Hiram, who are you?"'
```

**Expected**: Response should contain "I am"

Notice that the agent doesn't remember you in the next message:

```bash
curl -NsSfX POST http://localhost:9090/chat \
  -H "Content-Type: application/json" \
  -d '"Who am I?"'
```

**Expected**: Response should NOT contain "Hiram"

This is because we haven't added memory yet!

</TestScenario>

## Step 2: Add Memory Service

**Starting checkpoint**: [spring/examples/doc-checkpoints/02-with-memory](...)

Update your controller to accept a conversation ID:

<CodeFromFile
  file="spring/examples/doc-checkpoints/02-with-memory/src/main/java/com/example/demo/ChatController.java"
  before={3}
  after={12}
  lang="java"
>@PostMapping</CodeFromFile>

Add the security configuration for OAuth2:

<CodeFromFile
  file="spring/examples/doc-checkpoints/02-with-memory/src/main/java/com/example/demo/SecurityConfig.java"
  lang="java"
/>

Now test with memory:

<TestScenario checkpoint="spring/examples/doc-checkpoints/02-with-memory">

Start memory-service:

```bash
docker compose up -d
```

Get an auth token:

```bash
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
```

First message - introduce yourself:

```bash
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Hi, I'\''m Hiram, who are you?"'
```

**Expected**: Response should contain "I am"

Second message - test that the agent remembers:

```bash
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Who am I?"'
```

**Expected**: Response should contain "Hiram"

<Steps>
And the response status should be 200
And the response body should be json:
"""
{
  "id": "${response.body.id}",
  "role": "assistant",
  "content": "${response.body.content}"
}
"""
</Steps>

The agent now remembers your name!

</TestScenario>

Continue to [Conversation History](./conversation-history.mdx) to learn about retrieving and managing conversation data.
````

### What Happens Behind the Scenes

1. **At Build Time:**
   - `<CodeFromFile>` components read actual source code from checkpoints
   - `<TestScenario>` blocks extract test data to JSON:
     - Bash code blocks become curl commands
     - `**Expected**:` lines become simple assertions (string contains/not contains)
     - Optional `<Steps>` blocks provide advanced validation (JSON comparison with variable substitution)
     - Content renders unchanged (no visual modifications)
   - Test data is written to `site-tests/src/test/resources/test-scenarios.json`
   - Site builds with real code examples

2. **At Test Time (`./mvnw test -Ptest-docs`):**
   - Docker compose starts memory-service and Keycloak
   - For each `<TestScenario>`:
     - Checkpoint is built with Maven
     - Application starts on port 9090
     - Each bash block is executed sequentially
     - Response assertions verify expected behavior
     - Application is stopped
   - Feature file generated:
     ```gherkin
     # Generated from: getting-started.mdx
     Feature: Spring Getting Started Tutorial

       Background:
         Given the memory-service is running via docker compose
         And I set up authentication tokens

       Scenario: Step 1 - Create a Simple Agent
         Given I have checkpoint "spring/examples/doc-checkpoints/01-basic-agent"
         When I build the checkpoint
         Then the build should succeed
         When I start the checkpoint on port 9090
         Then the application should be running

         When I execute curl command:
           """
           curl -NsSfX POST http://localhost:9090/chat \
             -H "Content-Type: application/json" \
             -d '"Hi, I'\''m Hiram, who are you?"'
           """
         Then the response should contain "I am"

         When I execute curl command:
           """
           curl -NsSfX POST http://localhost:9090/chat \
             -H "Content-Type: application/json" \
             -d '"Who am I?"'
           """
         Then the response should not contain "Hiram"

         When I stop the checkpoint

       # ... more scenarios ...
     ```

3. **In CI (GitHub Actions):**
   - Workflow triggers on changes to docs or checkpoints
   - Runs full test suite
   - Fails PR if any documented command doesn't work
   - Uploads Cucumber HTML reports as artifacts

### Benefits of This Approach

- **Single source of truth**: Code lives in checkpoints, docs reference it
- **Perfect synchronization**: Tested commands are exactly what users see
- **Impossible to get out of sync**: Docs and tests are the same text
- **Author-friendly**: Simple markup, clear expectations
- **User-friendly**: "✓ Tested" badges build confidence
- **CI-validated**: Every tutorial is continuously verified

---

## Implementation Plan

### Phase 1: MDX Code Components (Week 1)

1. Create `CodeFromFile.astro` component with full feature set
2. Update `getting-started.mdx` as proof of concept
3. Document component usage for doc authors
4. Add TypeScript types for component props

### Phase 2: Documentation Test Infrastructure (Weeks 2-3)

1. Create `TestScenario.astro` component to emit test data during build
2. Create `site-tests` Maven module with proper structure
3. Implement `TestScenarioLoader` (simple JSON reader, no MDX parsing!)
4. Implement `CurlParser` to convert curl commands to Java HttpClient
5. Create base step definitions:
   - `DockerSteps`: Manage docker compose lifecycle
   - `CheckpointSteps`: Build and run checkpoint applications
   - `CurlSteps`: Execute curl commands and verify responses (including JSON validation with variable substitution)
6. Set up test runner and Maven profile (`test-docs`)

### Phase 3: Test Generation and Validation (Weeks 3-4)

1. Implement simplified `TestGenerator` that reads JSON from build
2. Mark existing Spring tutorial sections with `<TestScenario>` tags
3. Mark existing Quarkus tutorial sections with `<TestScenario>` tags
4. Run `npm run build` in site/ to generate test-scenarios.json
5. Generate Cucumber feature files from JSON
6. Validate generated tests run successfully locally
7. Add GitHub Actions workflow that builds site then runs tests

### Phase 4: Enhancement and Polish (Week 5)

1. Add support for bash functions (handle `get-token` pattern)
2. Improve error messages and debugging output in step definitions
3. Add Cucumber HTML reports and test coverage metrics
4. Create doc author guidelines for writing testable scenarios
5. Add optional "Tested ✓" badges next to tested sections


### Success Metrics

**Code Inclusion:**
- ✅ All code examples sourced from checkpoint files via `<CodeFromFile>` components
- ✅ Build fails fast if referenced files don't exist or match strings aren't found
- ✅ Code stays in sync automatically when checkpoints are updated

**Tutorial Testing:**
- ✅ All critical tutorial commands wrapped in `<TestScenario>` blocks
- ✅ 100% of marked scenarios generate and run successfully in CI
- ✅ Tests run automatically when docs or checkpoints change
- ✅ CI fails if documented commands don't work
- ✅ Zero drift between documented and tested commands (they're the same)
- ✅ Feature files regenerated automatically from docs

**Maintenance:**
- ✅ Simple for authors: just add `<TestScenario>` tags around testable sections
- ✅ Fast local iteration: authors can run `./mvnw test -Ptest-docs -Dtest=SpringGettingStartedTest`

## Open Questions

1. **How often to run full integration tests?**
   - **Recommended**: On doc/checkpoint changes only (via GitHub Actions path filters)
   - Optional: Nightly full runs to catch drift
   - Cost: ~30-45 minutes for full test suite

2. **Should we test with real OpenAI API or mocks?**
   - **Recommended**: Mock for CI (use MockLLM or fixed responses)
   - Real API only for manual verification before releases
   - Rationale: Fast, deterministic, free, reliable

3. **How to handle non-deterministic LLM responses?**
   - **Recommended**: Use mock LLM or fixed responses in CI tests
   - Check only that endpoints respond and return expected structure
   - Don't validate exact AI-generated text, just that keywords appear

4. **What to do when tests fail?**
   - **Recommended**: Block doc site deployment (fail CI)
   - Create GitHub issue automatically with failure details
   - Allows emergency doc fixes via `[skip ci]` if needed

## Related Enhancements

- [Enhancement 038: Tutorial Checkpoints](./038-tutorial-checkpoints.md) - Creates the checkpoint directories this references
- [Enhancement 012: Spring Site Docs](./012-spring-site-docs.md) - Original tutorial documentation
