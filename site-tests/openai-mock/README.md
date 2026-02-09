# OpenAI Mock Server (WireMock)

A configurable OpenAI API mock server using WireMock for deterministic testing.

## Features

- ✅ OpenAI-compatible API endpoints
- ✅ Configurable request/response mappings
- ✅ Dynamic response templating with Handlebars
- ✅ Request matching by URL, headers, body content
- ✅ Deterministic responses for reliable testing

## Configuration

### Response Mappings

Edit files in `mappings/` to customize responses:

- `models.json` - List available models
- `chat-completions.json` - Configure chat completion responses

### Response Templating

WireMock supports Handlebars templating for dynamic responses:

```json
{
  "content": "{{jsonPath request.body '$.messages[0].content'}}"
}
```

Available helpers:
- `{{now}}` - Current Unix timestamp
- `{{randomValue length=20 type='ALPHANUMERIC'}}` - Random string
- `{{jsonPath request.body '$.path'}}` - Extract from request body

### Custom Responses Based on Input

To return different responses based on input, create multiple mappings with different `bodyPatterns`:

```json
{
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
    "jsonBody": {
      "choices": [{
        "message": {
          "content": "I am an AI assistant..."
        }
      }]
    }
  }
}
```

## Building

```bash
docker build -t memory-service-openai-mock:latest .
```

## Running Standalone

```bash
docker run -p 8090:8080 memory-service-openai-mock:latest
```

## Testing

```bash
# List models
curl http://localhost:8090/v1/models

# Chat completion
curl -X POST http://localhost:8090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}'
```

## References

- [WireMock Documentation](https://wiremock.org/)
- [WireMock Response Templating](https://wiremock.org/docs/response-templating/)
- [OpenAI API Reference](https://platform.openai.com/docs/api-reference)
