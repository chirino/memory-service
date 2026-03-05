import express from "express";
import { createOpenAI } from "@ai-sdk/openai";
import { generateText } from "ai";
import { withMemoryService, withProxy } from "@chirino/memory-service-vercelai";

const app = express();
app.use(express.text({ type: "*/*" }));

function openAIBaseUrl(): string | undefined {
  const raw = process.env.OPENAI_BASE_URL;
  if (!raw) {
    return undefined;
  }
  const trimmed = raw.replace(/\/$/, "");
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

function asNumber(value: unknown): number | null {
  if (typeof value !== "string" || value === "") {
    return null;
  }
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
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

  const forkedAtConversationId =
    (req.query.forkedAtConversationId as string | undefined) ?? null;
  const forkedAtEntryId =
    (req.query.forkedAtEntryId as string | undefined) ?? null;

  const authorization = req.header("authorization") ?? null;

  const provider = createOpenAI({
    baseURL: openAIBaseUrl(),
    apiKey: process.env.OPENAI_API_KEY ?? "not-needed-for-tests",
  });
  const model = provider.chat(process.env.OPENAI_MODEL ?? "mock-gpt-markdown");

  const result = await withMemoryService(
    {
      conversationId,
      authorization,
      memoryContentType: "vercelai",
      userText: userMessage,
      forkedAtConversationId,
      forkedAtEntryId,
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

app.get("/v1/conversations/:conversationId/forks", async (req, res) => {
  await withProxy(req, res, (proxy) =>
    proxy.listConversationForks(req.params.conversationId, {
      afterCursor: (req.query.afterCursor as string | undefined) ?? null,
      limit: asNumber(req.query.limit),
    }),
  );
});

const port = Number(process.env.PORT ?? 9090);
app.listen(port, "0.0.0.0", () => {
  console.log(`listening on ${port}`);
});
