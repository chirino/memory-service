# Memory Service Processors

Memory Service processors are standalone operational roles that consume the gRPC event stream, do bounded derived work, and persist progress through admin checkpoints. They ship in the `memory-service` binary but run outside the serving path.

## Turn Traces

Start the turn-trace processor with:

```bash
memory-service process turn-traces \
  --endpoint localhost:8082 \
  --client-id turn-traces \
  --api-key "$MEMORY_SERVICE_API_KEY" \
  --bearer-token "$MEMORY_SERVICE_BEARER_TOKEN" \
  --scope admin
```

The processor subscribes to `entry` and `conversation` gRPC events with `detail=full`, detects conversation turns, emits one OpenTelemetry root span per closed turn, emits a child `memory-service.llm` generation span when context entries are observed, and stores a checkpoint with content type:

```text
application/vnd.memory-service.turn-trace-checkpoint+json;v=1
```

Useful flags:

| Flag | Purpose |
| --- | --- |
| `--scope admin|user` | Select admin-wide event visibility or authenticated-user membership filtering. |
| `--after-cursor start` | Bootstrap from the oldest retained outbox event when no checkpoint exists. |
| `--checkpoint-interval 5s` | Maximum interval between checkpoint flushes while work is advancing. |
| `--langfuse-name memory-service.turn` | Langfuse trace name and root span name. |
| `--idle-timeout 5m` | Close an open turn after no relevant events arrive. |
| `--max-turn-age 30m` | Force close long-running turns. |
| `--max-open-turns 1000` | Bound open-turn state stored in the checkpoint. |
| `--dry-run` | Process and checkpoint normally, but log turn boundaries instead of exporting spans. |

Direct Langfuse export uses OTLP HTTP environment variables:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=https://cloud.langfuse.com/api/public/otel
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic ${AUTH_STRING},x-langfuse-ingestion-version=4"
```

The root span exports turn input/output text from history entries to Langfuse trace and observation input/output fields. The child `memory-service.llm` span uses context entry text as generation input and the closing AI history text as generation output. The processor also exports IDs, cursors, counts, status, timestamps, and processor identity. It does not export attachment bytes or provider payloads that are not already represented in the Memory Service entries.

## In-Process Lifecycle

BDD and integration tests should start processors through the same lifecycle API used by the CLI instead of shelling out:

```go
running, err := turntraces.StartProcessor(ctx, turntraces.StartOptions{
    ClientID: "turn-traces-test",
    Scope:    "user",
    Events:   fakeEvents,
    Checkpoints: fakeCheckpoints,
    Sink:     fakeSpanSink,
    TurnTraces: turntraces.Config{
        DryRun: true,
    },
})
if err != nil {
    return err
}
defer running.Shutdown(context.Background())
```

Use injected `Events`, `Checkpoints`, and `Sink` for deterministic tests. When those fields are omitted, `StartProcessor` dials the configured gRPC endpoint and uses `EventStreamService`, `AdminCheckpointService`, and the configured OpenTelemetry exporter.
