import express from "express";
import { createOpenAI } from "@ai-sdk/openai";
import { generateText } from "ai";
import {
  createMemoryServiceProxy,
  memoryServiceConfigFromEnv,
  withMemoryService,
} from "@chirino/memory-service-vercelai";

const app = express();
app.use(express.text({ type: "*/*" }));
const memoryServiceConfig = memoryServiceConfigFromEnv();
const proxy = createMemoryServiceProxy(memoryServiceConfig);

function openAIBaseUrl(): string | undefined {
  const raw = process.env.OPENAI_BASE_URL;
  if (!raw) {
    return undefined;
  }
  const trimmed = raw.replace(/\/$/, "");
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

app.get("/ready", (_req, res) => {
  res.json({ status: "ok" });
});

app.post("/chat/:conversationId", async (req, res) => {
  const conversationId = req.params.conversationId;
  const userMessage = String(req.body ?? "").trim();
  if (!userMessage) {
    res.status(400).send("message is required");
    return;
  }

  const authorization = req.header("authorization") ?? null;

  const provider = createOpenAI({
    baseURL: openAIBaseUrl(),
    apiKey: process.env.OPENAI_API_KEY ?? "not-needed-for-tests",
  });
  const model = provider.chat(process.env.OPENAI_MODEL ?? "mock-gpt-markdown");

  const result = await withMemoryService(
    {
      ...memoryServiceConfig,
      conversationId,
      authorization,
      userText: userMessage,
      memoryContentType: "vercelai",
    },
    async (contextMemory) => {
      contextMemory.append({ role: "user", content: userMessage });
      const generated = await generateText({
        model,
        messages: [
          {
            role: "system",
            content: "You are a TypeScript memory-service demo agent.",
          },
          ...contextMemory.get(),
        ],
      });
      contextMemory.append({ role: "assistant", content: generated.text });
      return generated;
    },
  );
  const assistantText = result.text;

  res.type("text/plain").send(assistantText);
});

// SSE events proxy — streams real-time events from Memory Service
app.get("/v1/events", async (req, res) => {
  const kinds = req.query.kinds as string | undefined;

  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("Connection", "keep-alive");
  res.flushHeaders();

  try {
    for await (const event of proxy.streamEvents(kinds)) {
      res.write(`data: ${JSON.stringify(event)}\n\n`);
    }
  } catch {
    // Client disconnected or upstream closed
  } finally {
    res.end();
  }
});

const port = Number(process.env.PORT ?? 9090);
app.listen(port, "0.0.0.0", () => {
  console.log(`listening on ${port}`);
});
