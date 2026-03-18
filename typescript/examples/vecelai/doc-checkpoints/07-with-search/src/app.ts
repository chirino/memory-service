import express from "express";
import { createOpenAI } from "@ai-sdk/openai";
import { generateText } from "ai";
import {
  memoryServiceConfigFromEnv,
  withMemoryService,
  withProxy,
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

function indexedContent(text: string, _role: "USER" | "AI"): string {
  return text;
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
      memoryContentType: "vercelai",
      userText: userMessage,
      indexer: (message) => indexedContent(message.text, message.role),
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

app.post("/v1/conversations/search", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.searchConversations(req.body ?? {}),
  );
});

const port = Number(process.env.PORT ?? 9090);
app.listen(port, "0.0.0.0", () => {
  console.log(`listening on ${port}`);
});
