---
name: testing
description: Use when writing or debugging tests for memory-service. Covers Cucumber BDD patterns and failure reporting.
---

# Testing Guidelines

## Prefer Cucumber for API Testing
For REST and gRPC tests, use Cucumber feature files in `memory-service/src/test/resources/features/` instead of unit tests with mocks.

Reserve unit tests with mocks for internal implementation details and infrastructure testing.

## Cucumber Failure Reporting
When tests fail:
```bash
memory-service/src/test/resources/extract-failures.sh
```

Check `memory-service/target/cucumber/failures.txt` for structured failure summary with error messages and stack traces.
