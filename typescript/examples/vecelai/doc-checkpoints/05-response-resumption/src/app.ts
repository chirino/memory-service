import express from "express";
import { createOpenAI } from "@ai-sdk/openai";
import { streamText } from "ai";
import {
  memoryServiceConfigFromEnv,
  memoryServiceCancel,
  memoryServiceReplay,
  memoryServiceResumeCheck,
  withMemoryService,
} from "@chirino/memory-service-vercelai";

const app = express();
app.use(express.text({ type: "*/*" }));
app.use(express.json());
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
        memoryContentType: "vercelai",
        historyContentType: "history/vercelai",
        userText: userMessage,
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
        const eventStream = responseRecorder.record(
          result.textStream,
          (chunk) => ({
            eventType: "PartialResponse",
            text: chunk,
          }),
        );
        let assistantText = "";
        for await (const event of eventStream) {
          if (responseRecorder.isCanceled()) {
            break;
          }
          if (
            event.eventType === "PartialResponse" &&
            typeof event.text === "string"
          ) {
            assistantText += event.text;
          }
          res.write(`data: ${JSON.stringify(event)}\n\n`);
        }
        contextMemory.append({ role: "assistant", content: assistantText });
      },
    );
  } finally {
    res.end();
  }
});

app.post("/v1/conversations/resume-check", async (req, res) => {
  const ids = Array.isArray(req.body)
    ? req.body.filter((v) => typeof v === "string")
    : [];
  res.status(200).json(await memoryServiceResumeCheck(memoryServiceConfig, ids));
});

app.get("/v1/conversations/:conversationId/resume", async (req, res) => {
  let streamed = false;
  try {
    const eventStream = memoryServiceReplay(
      memoryServiceConfig,
      req.params.conversationId,
    );
    res.setHeader("Content-Type", "text/event-stream");
    res.setHeader("Cache-Control", "no-cache");
    res.setHeader("Connection", "keep-alive");
    streamed = true;
    for await (const event of eventStream) {
      res.write(`data: ${JSON.stringify(event)}\n\n`);
    }
  } catch {
    res.status(404).json({ error: "no in-progress response" });
  } finally {
    if (streamed && !res.writableEnded) {
      res.end();
    }
  }
});

app.post("/v1/conversations/:conversationId/cancel", async (req, res) => {
  await memoryServiceCancel(memoryServiceConfig, req.params.conversationId);
  res.status(204).send();
});

const port = Number(process.env.PORT ?? 9090);
app.listen(port, "0.0.0.0", () => {
  console.log(`listening on ${port}`);
});
