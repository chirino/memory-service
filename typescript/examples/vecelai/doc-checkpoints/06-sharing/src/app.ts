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

app.get("/v1/conversations/:conversationId/memberships", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.listMemberships(req.params.conversationId),
  );
});

app.post("/v1/conversations/:conversationId/memberships", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.createMembership(req.params.conversationId, req.body ?? {}),
  );
});

app.patch(
  "/v1/conversations/:conversationId/memberships/:userId",
  async (req, res) => {
    await withProxy(req, res, memoryServiceConfig, (proxy) =>
      proxy.updateMembership(
        req.params.conversationId,
        req.params.userId,
        req.body ?? {},
      ),
    );
  },
);

app.delete(
  "/v1/conversations/:conversationId/memberships/:userId",
  async (req, res) => {
    await withProxy(req, res, memoryServiceConfig, (proxy) =>
      proxy.deleteMembership(req.params.conversationId, req.params.userId),
    );
  },
);

app.get("/v1/ownership-transfers", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.listOwnershipTransfers({
      role: (req.query.role as string | undefined) ?? null,
      afterCursor: (req.query.afterCursor as string | undefined) ?? null,
      limit: asNumber(req.query.limit),
    }),
  );
});

app.post("/v1/ownership-transfers", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.createOwnershipTransfer(req.body ?? {}),
  );
});

app.post("/v1/ownership-transfers/:transferId/accept", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.acceptOwnershipTransfer(req.params.transferId),
  );
});

app.delete("/v1/ownership-transfers/:transferId", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.deleteOwnershipTransfer(req.params.transferId),
  );
});

const port = Number(process.env.PORT ?? 9090);
app.listen(port, "0.0.0.0", () => {
  console.log(`listening on ${port}`);
});
