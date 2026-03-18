import express from "express";
import { createOpenAI } from "@ai-sdk/openai";
import { streamText } from "ai";
import {
  memoryServiceConfigFromEnv,
  withMemoryService,
} from "@chirino/memory-service-vercelai";

const app = express();
app.use(express.text({ type: "*/*" }));
const memoryServiceConfig = memoryServiceConfigFromEnv();

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

  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("Connection", "keep-alive");

  try {
    await withMemoryService(
      {
        ...memoryServiceConfig,
        conversationId,
        authorization,
        userText: userMessage,
        memoryContentType: "vercelai",
      },
      async (contextMemory, responseRecorder) => {
        contextMemory.append({ role: "user", content: userMessage });
        const result = streamText({
          model,
          messages: [
            {
              role: "system",
              content: "You are a TypeScript memory-service demo agent.",
            },
            ...contextMemory.get(),
          ],
        });
        const eventStream = responseRecorder.record(result.textStream);
        for await (const event of eventStream) {
          const payload =
            typeof event === "object" &&
            event !== null &&
            typeof (event as { chunk?: unknown }).chunk === "string"
              ? { text: (event as { chunk: string }).chunk }
              : event;
          res.write(`data: ${JSON.stringify(payload)}\n\n`);
        }
        contextMemory.append({ role: "assistant", content: await result.text });
      },
    );
  } finally {
    res.end();
  }
});

const port = Number(process.env.PORT ?? 9090);
app.listen(port, "0.0.0.0", () => {
  console.log(`listening on ${port}`);
});
