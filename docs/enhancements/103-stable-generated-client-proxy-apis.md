---
status: proposed
---

# Enhancement 103: Stable Proxy APIs for Generated Clients

> **Status**: Proposed.

## Summary

Replace public helper/proxy methods that expose generated OpenAPI client positional arguments with stable request/option objects. Generated clients remain internal transport details, while Quarkus, Spring, examples, and docs call stable proxy APIs that do not break when optional query parameters are added.

## Motivation

The generated Java OpenAPI clients use positional method signatures for query parameters. Adding a new optional query argument changes the generated method signature and can break unrelated call sites. The recent `upToEntryId` addition to `listConversationEntries` showed the problem:

- Spring wrappers still called `listConversationEntriesWithHttpInfo(conversationId, afterCursor, limit, channel, epoch, forks)`, causing Java compilation failures.
- Quarkus unix-socket routing used reflection over generated method `args[n]`; the new optional argument shifted indexes and produced a runtime request with `limit=history`, causing `{"error":"invalid channel"}`.
- Example apps and extension code have to be rechecked any time optional generated query parameters are inserted.

The generated clients should be allowed to change with the OpenAPI contract, but our public helper APIs should not mirror that unstable shape.

## Design

### Principle

Generated OpenAPI clients are internal adapters. Public framework helpers expose stable request/option objects with named fields.

This applies to:

- Quarkus extension runtime helpers under `java/quarkus/memory-service-extension/runtime`
- Spring REST and boot helpers under `java/spring`
- demo app proxy controllers that forward frontend-safe endpoints
- handwritten unix-socket adapters that currently map generated method arguments by index

### Before

```java
proxy.listConversationEntries(
    conversationId,
    afterCursor,
    limit,
    Channel.HISTORY,
    null,
    "all");
```

This is fragile because the argument positions have to match the current generated client signature.

### After

```java
proxy.listConversationEntries(
    ListConversationEntriesOptions.builder(conversationId)
        .afterCursor(afterCursor)
        .limit(limit)
        .channel(Channel.HISTORY)
        .forks("all")
        .build());
```

The proxy implementation is the only place that calls the generated client:

```java
api.listConversationEntriesWithHttpInfo(
    options.conversationId(),
    options.afterCursor(),
    options.upToEntryId(),
    options.limit(),
    options.channel(),
    options.epoch(),
    options.forks());
```

When a new optional query parameter is added later, only the options type and adapter implementation need updates. Existing call sites remain source-compatible unless they need to use the new field.

### Options Type Shape

Create small endpoint-specific options objects rather than one generic map. Endpoint-specific types keep Java call sites discoverable and type-safe.

```java
public record ListConversationEntriesOptions(
    String conversationId,
    UUID afterCursor,
    UUID upToEntryId,
    Integer limit,
    Channel channel,
    String epoch,
    String forks) {

    public static Builder builder(String conversationId) {
        return new Builder(conversationId);
    }

    public static final class Builder {
        // fluent setters for optional fields
    }
}
```

Use equivalent package-local or public types in both Java stacks as appropriate:

| Stack | Proposed type |
| --- | --- |
| Quarkus | `io.github.chirino.memory.runtime.ListConversationEntriesOptions` |
| Spring | `io.github.chirino.memoryservice.client.ListConversationEntriesOptions` |

### Proxy Compatibility

Pre-release compatibility is not required, so delete fragile positional helper overloads instead of keeping deprecated wrappers. Example apps and docs should be updated to use the option-object API.

Generated client method calls are still positional, but they should exist only inside small adapter methods. That makes future OpenAPI changes fail in one predictable location during compile instead of across examples and documentation checkpoints.

### Unix-Socket Adapter

The current Quarkus unix-socket adapter dispatches by generated interface method name and manually maps `args[n]` to query parameters. For endpoints with several optional query parameters, add a handwritten adapter method that accepts the stable options object and emits query parameters from named fields.

If the dynamic proxy remains, keep generated positional mapping private to the adapter and add tests that verify emitted query strings when optional parameters are present.

### Rollout Order

1. Introduce `ListConversationEntriesOptions` in Quarkus and Spring helper packages.
2. Change `MemoryServiceProxy.listConversationEntries` in both stacks to accept the options object.
3. Update Quarkus and Spring example controllers to construct the options object.
4. Update docs snippets to use the options object.
5. Move generated positional calls into one adapter method per stack.
6. Remove old positional helper overloads.

## Testing

Add compile and behavior tests that would have caught the `upToEntryId` break.

```gherkin
Feature: stable Java proxy APIs

  Scenario: Quarkus proxy lists history entries through stable options
    Given a Quarkus example app uses ListConversationEntriesOptions
    When the app lists conversation entries with channel history and forks all
    Then the request sent to memory-service includes channel "history"
    And the request sent to memory-service includes forks "all"
    And no optional query parameter is shifted into another field

  Scenario: Spring proxy lists context entries through stable options
    Given a Spring memory repository uses ListConversationEntriesOptions
    When the repository loads context entries
    Then the generated client receives channel CONTEXT
    And the generated client receives a null upToEntryId unless configured
```

Unit tests should cover the unix-socket query builder directly, because that is where positional generated arguments have historically caused runtime-only failures.

## Tasks

- [ ] Add Quarkus `ListConversationEntriesOptions`.
- [ ] Add Spring `ListConversationEntriesOptions`.
- [ ] Convert Quarkus `MemoryServiceProxy.listConversationEntries` to the options API.
- [ ] Convert Spring `MemoryServiceProxy.listConversationEntries` to the options API.
- [ ] Update Java example proxy controllers and chat apps.
- [ ] Update docs snippets that show Java proxy entry listing.
- [ ] Add unix-socket adapter tests for entry-listing query parameter mapping.
- [ ] Add targeted compile checks for affected Quarkus and Spring modules.

## Files to Modify

| File | Change |
| --- | --- |
| `java/quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/runtime/MemoryServiceProxy.java` | Replace positional entry-listing helper with options-object API. |
| `java/quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/runtime/ListConversationEntriesOptions.java` | New Quarkus options type. |
| `java/quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/runtime/UnixSocketRestClientFactory.java` | Stop depending on shifted generated argument indexes for entry-listing query construction, or test that mapping explicitly. |
| `java/quarkus/examples/**/ConversationsResource.java` | Update examples to use the options object. |
| `java/spring/memory-service-rest-spring/src/main/java/io/github/chirino/memoryservice/client/MemoryServiceProxy.java` | Replace positional entry-listing helper with options-object API. |
| `java/spring/memory-service-rest-spring/src/main/java/io/github/chirino/memoryservice/client/ListConversationEntriesOptions.java` | New Spring options type. |
| `java/spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/memory/MemoryServiceChatMemoryRepository.java` | Use the stable Spring options API. |
| `java/spring/examples/**/MemoryServiceProxyController.java` | Update examples to use the options object. |
| `site/src/pages/docs/quarkus/*.mdx` | Update Quarkus Java snippets. |
| `site/src/pages/docs/spring/*.mdx` | Update Spring Java snippets. |
| `java/quarkus/FACTS.md` and `java/spring/FACTS.md` | Record the generated-client wrapper rule after implementation. |

## Verification

```bash
# Compile affected Java helper modules and examples.
./java/mvnw -B -q -f java/pom.xml \
  -pl :memory-service-extension,:memory-service-rest-spring,:memory-service-spring-boot-autoconfigure \
  -am compile -DskipTests > java-client-compile.log 2>&1
rg -n "ERROR|FAILURE|Compilation failure|cannot be applied|incompatible types" java-client-compile.log

# Run site docs scenarios, which exercise Java docs checkpoints.
task test:site > site-test.log 2>&1
rg -n "FAIL|ERROR|failed|invalid channel" site-test.log
```

## Non-Goals

- Do not change the OpenAPI generator or generated client method shape.
- Do not add backward-compatible deprecated positional helper overloads.
- Do not convert every generated client call in one pass; start with entry listing and apply the pattern to endpoints that gain optional query parameters.

## Design Decisions

- Endpoint-specific options objects are preferred over `Map<String, Object>` so call sites remain type-safe and discoverable.
- Generated clients remain useful as transport adapters, but their positional signatures should not leak into framework helper APIs or examples.
- Runtime adapters that cannot avoid generated positional calls must be covered by tests that assert the resulting HTTP query parameters.

## Research: OpenAPI Generator Support

Before implementing handwritten wrapper APIs, research whether OpenAPI Generator can generate a stable builder-style or request-object API directly for the Java clients we use.

Questions to answer:

| Question | Notes |
| --- | --- |
| Can the selected Java generators emit operation request builders instead of positional operation methods? | Check the generators used by `memory-service-rest-quarkus` and `memory-service-rest-spring`, including MicroProfile and Spring/WebClient variants. |
| Is there an option equivalent to `useSingleRequestParameter` for Java? | Some OpenAPI generator targets support wrapping operation parameters in one request object; verify whether that exists for our Java generators and whether it covers query parameters. |
| Can templates be customized to add a generated builder layer while preserving the underlying generated client? | A template override may be viable if a built-in option does not exist. |
| Would generated request objects be stable when optional query parameters are added? | The key requirement is source compatibility for call sites that do not use the new optional parameter. |
| Does the generated API remain ergonomic for examples and docs? | If generated builders are noisy or generator-specific, handwritten framework helpers may still be clearer. |
| How would the unix-socket adapter interact with generated builders? | If the adapter still reflects over positional generated methods, the runtime failure mode remains. |

The preferred outcome is to configure generation so the stable API is generated consistently. If the generator cannot provide a source-compatible builder/request-object surface for the Java clients, implement the handwritten options objects described above and document the generator limitation in `java/quarkus/FACTS.md` and `java/spring/FACTS.md`.
