# Memory Service Processors

Memory Service processors are standalone operational roles that consume the gRPC event stream, do bounded derived work, and persist progress through admin checkpoints. They ship in the `memory-service` binary but run outside the serving path.

## Turn Traces

Start the turn-trace processor with:

```bash
memory-service process turn-traces \
  --endpoint localhost:8082 \
  --client-id turn-traces \
  --api-key "$MEMORY_SERVICE_API_KEY" \
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

## Authentication

Both `process turn-traces` and `process clickhouse` require an API key or OAuth2 client credentials. Configure one authentication mode. The processor client ID also identifies the OAuth client and checkpoint owner. The client needs the Memory Service `admin` role for admin event streams, checkpoints, and context lookups.

| Flag | Environment variable | Default | Purpose |
| --- | --- | --- | --- |
| `--client-id` | `MEMORY_SERVICE_PROCESS_CLIENT_ID` | Required | Processor identity and OAuth client ID. |
| `--api-key` | `MEMORY_SERVICE_API_KEY` | None | API key, mutually exclusive with OIDC. |
| `--oidc-issuer` | `MEMORY_SERVICE_PROCESS_OIDC_ISSUER` | None | Discover the token endpoint through `/.well-known/openid-configuration`. |
| `--oidc-token-url` | `MEMORY_SERVICE_PROCESS_OIDC_TOKEN_URL` | None | Explicit token endpoint instead of issuer discovery. |
| Environment only | `MEMORY_SERVICE_PROCESS_OIDC_CLIENT_SECRET` | None | OAuth client secret. |
| `--oidc-client-secret-file` | `MEMORY_SERVICE_PROCESS_OIDC_CLIENT_SECRET_FILE` | None | Secret file instead of the secret environment variable. |
| `--oidc-audience` | `MEMORY_SERVICE_PROCESS_OIDC_AUDIENCE` | None | OAuth `audience` form parameter. |
| `--oidc-scope` | `MEMORY_SERVICE_PROCESS_OIDC_SCOPE` | None | Repeatable scope flag, or comma-separated environment values. Sent space-separated. |
| `--oidc-ca-file` | `MEMORY_SERVICE_PROCESS_OIDC_CA_FILE` | None | PEM CA bundle for discovery and token requests. |
| `--oidc-tls-insecure-skip-verify` | `MEMORY_SERVICE_PROCESS_OIDC_TLS_INSECURE_SKIP_VERIFY` | `false` | Skip OIDC TLS verification for development. |
| `--allow-insecure-oidc` | `MEMORY_SERVICE_PROCESS_ALLOW_INSECURE_OIDC` | `false` | Permit non-loopback plaintext HTTP for development. |

OIDC requires exactly one endpoint setting and one secret source. Loopback HTTP endpoints are allowed. The `PROCESS_` environment prefix keeps server OIDC settings separate from processor authentication.

For example, use a mounted secret file with issuer discovery:

```bash
memory-service process turn-traces \
  --endpoint localhost:8082 \
  --client-id turn-traces \
  --oidc-issuer http://localhost:8080/realms/memory-service \
  --oidc-client-secret-file /run/secrets/processor-client-secret \
  --oidc-audience memory-service \
  --oidc-scope openid --oidc-scope profile --oidc-scope email
```

The processor obtains its first access token before startup completes and fails startup if the grant fails. Tokens must include a positive `expires_in`. One background worker renews the shared cached token before expiry with a new client-credentials grant. Requests and stream opens keep using a valid token while renewal is pending or fails. Only requests with expired or rejected tokens wait for renewal, and each wait respects the request cancellation or deadline. Concurrent requests share one renewal. A rejected token triggers one retry with a new token. A stream rejected after delivering events reconnects from the saved cursor. Later token endpoint failures retry with exponential backoff until the processor stops. A prolonged outage can still exhaust a checkpoint lease and stop the ClickHouse processor.

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
