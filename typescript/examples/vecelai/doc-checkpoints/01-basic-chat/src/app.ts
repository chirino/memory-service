import express from "express";
import { createOpenAI } from "@ai-sdk/openai";
import { generateText } from "ai";

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

app.get("/ready", (_req, res) => {
  res.json({ status: "ok" });
});

app.post("/chat", async (req, res) => {
  const userMessage = String(req.body ?? "").trim();
  if (!userMessage) {
    res.status(400).send("message is required");
    return;
  }

  const provider = createOpenAI({
    baseURL: openAIBaseUrl(),
    apiKey: process.env.OPENAI_API_KEY ?? "not-needed-for-tests",
  });
  const model = provider.chat(process.env.OPENAI_MODEL ?? "mock-gpt-markdown");

  const result = await generateText({
    model,
    messages: [
      {
        role: "system",
        content: "You are a TypeScript memory-service demo agent.",
      },
      { role: "user", content: userMessage },
    ],
  });
  const text = result.text;
  res.type("text/plain").send(text);
});

const port = Number(process.env.PORT ?? 9090);
app.listen(port, "0.0.0.0", () => {
  console.log(`listening on ${port}`);
});
