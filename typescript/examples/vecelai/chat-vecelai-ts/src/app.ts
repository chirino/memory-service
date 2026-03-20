import express from "express";
import { createOpenAI } from "@ai-sdk/openai";
import { streamText } from "ai";
import {
  createMemoryServiceProxy,
  memoryServiceConfigFromEnv,
  memoryServiceCancel,
  memoryServiceReplay,
  memoryServiceResumeCheck,
  withProxy,
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
        forkedAtConversationId,
        forkedAtEntryId,
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
          if (responseRecorder.isCanceled()) {
            break;
          }
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

app.get("/v1/conversations/:conversationId", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.getConversation(req.params.conversationId),
  );
});

app.get("/v1/conversations/:conversationId/entries", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.listConversationEntries(req.params.conversationId, {
      afterCursor: (req.query.afterCursor as string | undefined) ?? null,
      limit: asNumber(req.query.limit),
      channel: "history",
      forks: (req.query.forks as string | undefined) ?? null,
    }),
  );
});

app.get("/v1/conversations", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.listConversations({
      mode: (req.query.mode as string | undefined) ?? null,
      afterCursor: (req.query.afterCursor as string | undefined) ?? null,
      limit: asNumber(req.query.limit),
      query: (req.query.query as string | undefined) ?? null,
    }),
  );
});

app.get("/v1/conversations/:conversationId/forks", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.listConversationForks(req.params.conversationId, {
      afterCursor: (req.query.afterCursor as string | undefined) ?? null,
      limit: asNumber(req.query.limit),
    }),
  );
});

app.post("/v1/conversations/search", async (req, res) => {
  await withProxy(req, res, memoryServiceConfig, (proxy) =>
    proxy.searchConversations(req.body ?? {}),
  );
});

app.post("/v1/conversations/resume-check", async (req, res) => {
  const ids = Array.isArray(req.body)
    ? req.body.filter((v) => typeof v === "string")
    : [];
  res
    .status(200)
    .json(await memoryServiceResumeCheck(memoryServiceConfig, ids));
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

// SSE events proxy — streams real-time events from Memory Service
const eventsProxy = createMemoryServiceProxy(memoryServiceConfig);
app.get("/v1/events", async (req, res) => {
  const kinds = req.query.kinds as string | undefined;

  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("Connection", "keep-alive");
  res.flushHeaders();

  try {
    for await (const event of eventsProxy.streamEvents(kinds)) {
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
