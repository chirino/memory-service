import type { ModelMessage } from "ai";
import { Agent, type Dispatcher } from "undici";
import { createRequire } from "node:module";
import { isAbsolute } from "node:path";
import { fileURLToPath } from "node:url";

type HistoryRole = "USER" | "AI";
const MAX_MESSAGES_PER_MEMORY_ENTRY = 100;

type MemoryServiceProxyOptions = {
  baseUrl?: string;
  unixSocket?: string;
  apiKey?: string;
  authorization?: string | null;
};

type ListEntriesOptions = {
  afterCursor?: string | null;
  limit?: number | null;
  channel?: string | null;
  epoch?: string | null;
  forks?: string | null;
};

const memoryServiceDispatchers = new Map<string, Dispatcher>();

function resolveMemoryServiceUnixSocket(
  unixSocket?: string,
): string | undefined {
  const candidate = (
    unixSocket ??
    process.env.MEMORY_SERVICE_UNIX_SOCKET ??
    ""
  ).trim();
  if (!candidate) {
    return undefined;
  }
  if (!isAbsolute(candidate)) {
    throw new Error("MEMORY_SERVICE_UNIX_SOCKET must be an absolute path");
  }
  return candidate;
}

function memoryServiceBaseUrl(baseUrl?: string, unixSocket?: string): string {
  if (resolveMemoryServiceUnixSocket(unixSocket)) {
    return "http://localhost";
  }
  return (
    baseUrl ??
    process.env.MEMORY_SERVICE_URL ??
    "http://localhost:8082"
  ).replace(/\/$/, "");
}

function memoryServiceDispatcher(unixSocket?: string): Dispatcher | undefined {
  const socketPath = resolveMemoryServiceUnixSocket(unixSocket);
  if (!socketPath) {
    return undefined;
  }
  const existing = memoryServiceDispatchers.get(socketPath);
  if (existing) {
    return existing;
  }
  const created = new Agent({
    connect: {
      socketPath,
    },
  });
  memoryServiceDispatchers.set(socketPath, created);
  return created;
}

function memoryServiceApiKey(apiKey?: string): string {
  return apiKey ?? process.env.MEMORY_SERVICE_API_KEY ?? "agent-api-key-1";
}

function compactQuery(
  params: Record<string, string | number | null | undefined>,
): string {
  const qp = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    qp.set(key, String(value));
  }
  const text = qp.toString();
  return text ? `?${text}` : "";
}

function memoryServiceHeaders(
  options: MemoryServiceProxyOptions,
): Record<string, string> {
  const headers: Record<string, string> = {
    "X-API-Key": memoryServiceApiKey(options.apiKey),
  };
  if (options.authorization) {
    headers.Authorization = options.authorization;
  }
  return headers;
}

async function memoryServiceRequest(
  method: string,
  path: string,
  options: MemoryServiceProxyOptions & {
    body?: unknown;
    contentType?: string;
  },
): Promise<Response> {
  const url = `${memoryServiceBaseUrl(options.baseUrl, options.unixSocket)}${path}`;
  const headers = memoryServiceHeaders(options);
  if (options.contentType) {
    headers["Content-Type"] = options.contentType;
  }
  const init: RequestInit & { dispatcher?: Dispatcher } = {
    method,
    headers,
    body:
      options.body == null
        ? undefined
        : typeof options.body === "string"
          ? options.body
          : JSON.stringify(options.body),
  };
  const dispatcher = memoryServiceDispatcher(options.unixSocket);
  if (dispatcher) {
    init.dispatcher = dispatcher;
  }
  return fetch(url, init);
}

async function relayResponse(
  res: {
    status: (c: number) => any;
    setHeader: (n: string, v: string) => any;
    send: (b: any) => any;
  },
  upstream: Response,
): Promise<void> {
  const forwardHeaders = [
    "cache-control",
    "content-disposition",
    "content-type",
    "etag",
    "expires",
    "last-modified",
    "location",
    "pragma",
  ];
  for (const header of forwardHeaders) {
    const value = upstream.headers.get(header);
    if (value) {
      res.setHeader(header, value);
    }
  }
  const contentType = upstream.headers.get("content-type") ?? "";
  res.status(upstream.status);
  if (contentType.includes("application/json")) {
    res.send(await upstream.text());
    return;
  }
  const data = await upstream.arrayBuffer();
  res.send(Buffer.from(data));
}

export async function withProxy(
  req: { header: (name: string) => string | undefined },
  res: {
    status: (c: number) => any;
    setHeader: (n: string, v: string) => any;
    send: (b: any) => any;
  },
  call: (
    proxy: ReturnType<typeof createMemoryServiceProxy>,
  ) => Promise<Response>,
): Promise<void> {
  const proxy = createMemoryServiceProxy({
    authorization: req.header("authorization") ?? null,
  });
  await relayResponse(res, await call(proxy));
}

function createMemoryServiceProxy(options: MemoryServiceProxyOptions) {
  return {
    getConversation(conversationId: string): Promise<Response> {
      return memoryServiceRequest(
        "GET",
        `/v1/conversations/${conversationId}`,
        options,
      );
    },
    listConversations(query: {
      mode?: string | null;
      afterCursor?: string | null;
      limit?: number | null;
      query?: string | null;
    }): Promise<Response> {
      const qs = compactQuery(query);
      return memoryServiceRequest("GET", `/v1/conversations${qs}`, options);
    },
    listConversationEntries(
      conversationId: string,
      query: ListEntriesOptions,
    ): Promise<Response> {
      const qs = compactQuery(
        query as Record<string, string | number | null | undefined>,
      );
      return memoryServiceRequest(
        "GET",
        `/v1/conversations/${conversationId}/entries${qs}`,
        options,
      );
    },
    listConversationForks(
      conversationId: string,
      query: { afterCursor?: string | null; limit?: number | null } = {},
    ): Promise<Response> {
      const qs = compactQuery(query);
      return memoryServiceRequest(
        "GET",
        `/v1/conversations/${conversationId}/forks${qs}`,
        options,
      );
    },
    searchConversations(payload: unknown): Promise<Response> {
      return memoryServiceRequest("POST", "/v1/conversations/search", {
        ...options,
        body: payload,
        contentType: "application/json",
      });
    },
    listMemberships(conversationId: string): Promise<Response> {
      return memoryServiceRequest(
        "GET",
        `/v1/conversations/${conversationId}/memberships`,
        options,
      );
    },
    createMembership(
      conversationId: string,
      payload: unknown,
    ): Promise<Response> {
      return memoryServiceRequest(
        "POST",
        `/v1/conversations/${conversationId}/memberships`,
        { ...options, body: payload, contentType: "application/json" },
      );
    },
    updateMembership(
      conversationId: string,
      userId: string,
      payload: unknown,
    ): Promise<Response> {
      return memoryServiceRequest(
        "PATCH",
        `/v1/conversations/${conversationId}/memberships/${userId}`,
        { ...options, body: payload, contentType: "application/json" },
      );
    },
    deleteMembership(
      conversationId: string,
      userId: string,
    ): Promise<Response> {
      return memoryServiceRequest(
        "DELETE",
        `/v1/conversations/${conversationId}/memberships/${userId}`,
        options,
      );
    },
    listOwnershipTransfers(
      query: {
        role?: string | null;
        afterCursor?: string | null;
        limit?: number | null;
      } = {},
    ): Promise<Response> {
      const qs = compactQuery(query);
      return memoryServiceRequest(
        "GET",
        `/v1/ownership-transfers${qs}`,
        options,
      );
    },
    createOwnershipTransfer(payload: unknown): Promise<Response> {
      return memoryServiceRequest("POST", "/v1/ownership-transfers", {
        ...options,
        body: payload,
        contentType: "application/json",
      });
    },
    acceptOwnershipTransfer(transferId: string): Promise<Response> {
      return memoryServiceRequest(
        "POST",
        `/v1/ownership-transfers/${transferId}/accept`,
        options,
      );
    },
    deleteOwnershipTransfer(transferId: string): Promise<Response> {
      return memoryServiceRequest(
        "DELETE",
        `/v1/ownership-transfers/${transferId}`,
        options,
      );
    },
    cancelResponse(conversationId: string): Promise<Response> {
      return memoryServiceRequest(
        "DELETE",
        `/v1/conversations/${conversationId}/response`,
        options,
      );
    },
  };
}

type HistoryEntry = {
  role: HistoryRole;
  text: string;
  indexedContent?: string | null;
};

type MemoryEntry = {
  role: HistoryRole;
  text: string;
};

type AppendEntriesBaseOptions = MemoryServiceProxyOptions & {
  conversationId: string;
  forkedAtConversationId?: string | null;
  forkedAtEntryId?: string | null;
};

async function appendEntries(
  options: AppendEntriesBaseOptions & {
    channel: "history" | "memory";
    entries: Array<HistoryEntry | MemoryEntry>;
  },
): Promise<Response[]> {
  if (options.channel === "memory") {
    const response = await memoryServiceRequest(
      "POST",
      `/v1/conversations/${options.conversationId}/entries`,
      {
        ...options,
        contentType: "application/json",
        body: {
          channel: "memory",
          contentType: "vercelai",
          content: options.entries.map((entry) =>
            toModelMessage(entry as MemoryEntry),
          ),
          forkedAtConversationId: options.forkedAtConversationId ?? undefined,
          forkedAtEntryId: options.forkedAtEntryId ?? undefined,
        },
      },
    );
    return [response];
  }
  return Promise.all(
    options.entries.map((entry) =>
      memoryServiceRequest(
        "POST",
        `/v1/conversations/${options.conversationId}/entries`,
        {
          ...options,
          contentType: "application/json",
          body: {
            channel: "history",
            contentType: "history",
            content: [{ role: entry.role, text: entry.text }],
            indexedContent: (entry as HistoryEntry).indexedContent ?? undefined,
            forkedAtConversationId: options.forkedAtConversationId ?? undefined,
            forkedAtEntryId: options.forkedAtEntryId ?? undefined,
          },
        },
      ),
    ),
  );
}

async function appendHistoryEntry(
  options: AppendEntriesBaseOptions & {
    role: HistoryRole;
    text: string;
    indexedContent?: string | null;
  },
): Promise<Response> {
  const [response] = await appendEntries({
    ...options,
    channel: "history",
    entries: [
      {
        role: options.role,
        text: options.text,
        indexedContent: options.indexedContent ?? undefined,
      },
    ],
  });
  return response;
}

type HistoryEvent = Record<string, unknown>;

async function appendHistoryEventsEntry(
  options: AppendEntriesBaseOptions & {
    role: HistoryRole;
    text?: string | null;
    events: HistoryEvent[];
    indexedContent?: string | null;
    historyContentType: string;
  },
): Promise<Response> {
  return memoryServiceRequest(
    "POST",
    `/v1/conversations/${options.conversationId}/entries`,
    {
      ...options,
      contentType: "application/json",
      body: {
        channel: "history",
        contentType: options.historyContentType,
        content: [
          {
            role: options.role,
            text: options.text ?? undefined,
            events: options.events,
          },
        ],
        indexedContent: options.indexedContent ?? undefined,
        forkedAtConversationId: options.forkedAtConversationId ?? undefined,
        forkedAtEntryId: options.forkedAtEntryId ?? undefined,
      },
    },
  );
}

async function appendMemoryEntries(
  options: AppendEntriesBaseOptions & {
    entries: MemoryEntry[];
  },
): Promise<Response[]> {
  return appendEntries({ ...options, channel: "memory" });
}

async function appendMemoryMessagesChunked(
  options: AppendEntriesBaseOptions & {
    messages: ModelMessage[];
    memoryContentType: string;
  },
): Promise<Response[]> {
  const responses: Response[] = [];
  for (
    let i = 0;
    i < options.messages.length;
    i += MAX_MESSAGES_PER_MEMORY_ENTRY
  ) {
    const response = await memoryServiceRequest(
      "POST",
      `/v1/conversations/${options.conversationId}/entries`,
      {
        ...options,
        contentType: "application/json",
        body: {
          channel: "memory",
          contentType: options.memoryContentType,
          content: options.messages.slice(i, i + MAX_MESSAGES_PER_MEMORY_ENTRY),
          forkedAtConversationId: options.forkedAtConversationId ?? undefined,
          forkedAtEntryId: options.forkedAtEntryId ?? undefined,
        },
      },
    );
    responses.push(response);
  }
  return responses;
}

async function syncMemoryMessages(
  options: AppendEntriesBaseOptions & {
    messages: ModelMessage[];
    memoryContentType: string;
  },
): Promise<Response> {
  return memoryServiceRequest(
    "POST",
    `/v1/conversations/${options.conversationId}/entries/sync`,
    {
      ...options,
      contentType: "application/json",
      body: {
        channel: "memory",
        contentType: options.memoryContentType,
        content: options.messages,
      },
    },
  );
}

type ContextMemory = {
  get: () => ModelMessage[];
  append: (entry: ModelMessage) => void;
  clear: () => void;
  set: (entries: ModelMessage[]) => void;
};

type IndexedMessage = {
  role: HistoryRole;
  text: string;
};

export type WithMemoryServiceResponseRecorder = {
  record: {
    (chunk: string): void;
    <T>(
      stream: AsyncIterable<T>,
      mapper?: (item: T) => HistoryEvent | null,
    ): AsyncIterable<HistoryEvent>;
  };
  text: () => string;
  isCanceled: () => boolean;
  recordHistoryEvent: (event: HistoryEvent) => void;
  recordHistoryEvents: (events: HistoryEvent[]) => void;
  createVercelAiEventListeners: () => VercelAiEventListeners;
};

type VercelAiChunk = {
  type?: string;
  [key: string]: unknown;
};

export type VercelAiEventListeners = {
  onChunk: (event: { chunk: VercelAiChunk }) => void;
  onError: (event: { error: unknown }) => void;
  onAbort: () => void;
  onStepFinish: (event: Record<string, unknown>) => void;
  onFinish: (event: Record<string, unknown>) => void;
};

let grpcDependenciesAvailable: Promise<boolean> | null = null;
const requireFromHere = createRequire(import.meta.url);
const dynamicImport = new Function(
  "modulePath",
  "return import(modulePath)",
) as (modulePath: string) => Promise<unknown>;
let responseRecorderServicePromise: Promise<{
  grpc: any;
  ResponseRecorderService: any;
}> | null = null;
const RESPONSE_RECORDER_PROTO_PATH = fileURLToPath(
  new URL("../proto/memory_service.proto", import.meta.url),
);
const RESPONSE_RECORDER_COMPLETE_TIMEOUT_MS = 5_000;
const activeResponseStates = new Map<string, { canceled: boolean }>();

async function hasGrpcDependencies(): Promise<boolean> {
  if (grpcDependenciesAvailable) {
    return grpcDependenciesAvailable;
  }
  grpcDependenciesAvailable = (async () => {
    try {
      requireFromHere.resolve("@grpc/grpc-js");
      requireFromHere.resolve("@grpc/proto-loader");
      return true;
    } catch {
      return false;
    }
  })();
  return grpcDependenciesAvailable;
}

function resolveGrpcTarget(): string {
  const explicit = process.env.MEMORY_SERVICE_GRPC_TARGET;
  if (explicit) {
    return explicit;
  }
  const unixSocket = resolveMemoryServiceUnixSocket();
  if (unixSocket) {
    return `unix://${unixSocket}`;
  }
  const base = process.env.MEMORY_SERVICE_URL ?? "http://localhost:8082";
  try {
    const parsed = new URL(base);
    const port = parsed.port || (parsed.protocol === "https:" ? "443" : "80");
    return `${parsed.hostname}:${port}`;
  } catch {
    return base;
  }
}

function uuidToBytes(uuid: string): Buffer {
  const hex = uuid.replace(/-/g, "");
  if (!/^[0-9a-fA-F]{32}$/.test(hex)) {
    throw new Error(`invalid UUID: ${uuid}`);
  }
  return Buffer.from(hex, "hex");
}

function bytesToUuid(value: Buffer | Uint8Array): string {
  const hex = Buffer.from(value).toString("hex");
  if (hex.length !== 32) {
    throw new Error("invalid UUID byte length");
  }
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

async function loadResponseRecorderService(): Promise<{
  grpc: any;
  ResponseRecorderService: any;
}> {
  if (responseRecorderServicePromise) {
    return responseRecorderServicePromise;
  }
  responseRecorderServicePromise = (async () => {
    const grpc = (await dynamicImport("@grpc/grpc-js")) as any;
    const protoLoader = (await dynamicImport("@grpc/proto-loader")) as any;
    const packageDefinition = await protoLoader.load(
      RESPONSE_RECORDER_PROTO_PATH,
      {
        keepCase: true,
        longs: String,
        enums: String,
        defaults: true,
        oneofs: true,
      },
    );
    const loaded = grpc.loadPackageDefinition(packageDefinition) as any;
    return {
      grpc,
      ResponseRecorderService: loaded.memory.v1.ResponseRecorderService,
    };
  })();
  return responseRecorderServicePromise;
}

async function createResponseRecorderClient(target?: string): Promise<any> {
  const { grpc, ResponseRecorderService } = await loadResponseRecorderService();
  return new ResponseRecorderService(
    target ?? resolveGrpcTarget(),
    grpc.credentials.createInsecure(),
  );
}

async function grpcUnary<TResponse>(
  client: any,
  method: string,
  request: unknown,
): Promise<TResponse> {
  return new Promise<TResponse>((resolve, reject) => {
    client[method](request, (error: unknown, response: TResponse) => {
      if (error) {
        reject(error);
        return;
      }
      resolve(response);
    });
  });
}

class GrpcConversationRecorder {
  private readonly conversationIdBytes: Buffer;
  private call: any;
  private readonly writeChain: Promise<void> = Promise.resolve();
  private pendingWrites: Promise<void> = this.writeChain;
  private closed = false;
  private readonly responseDone: Promise<void>;

  constructor(client: any, conversationId: string) {
    this.conversationIdBytes = uuidToBytes(conversationId);
    this.responseDone = new Promise<void>((resolve, reject) => {
      this.call = client.Record((error: any, response: any) => {
        if (error) {
          reject(error);
          return;
        }
        if (
          response?.status === "RECORD_STATUS_ERROR" ||
          response?.status === 3
        ) {
          reject(
            new Error(response?.error_message || "response recording failed"),
          );
          return;
        }
        resolve();
      });
      this.call.on("error", reject);
    });
  }

  record(chunk: string): void {
    if (this.closed) {
      return;
    }
    this.pendingWrites = this.pendingWrites.then(
      () =>
        new Promise<void>((resolve, reject) => {
          this.call.write(
            {
              conversation_id: this.conversationIdBytes,
              content: chunk,
            },
            (error: any) => {
              if (error) {
                reject(error);
                return;
              }
              resolve();
            },
          );
        }),
    );
  }

  private async awaitWithTimeout<T>(
    promise: Promise<T>,
    timeoutMs: number,
  ): Promise<T | null> {
    let timeoutHandle: NodeJS.Timeout | undefined;
    try {
      return await Promise.race<T | null>([
        promise,
        new Promise<null>((resolve) => {
          timeoutHandle = setTimeout(() => resolve(null), timeoutMs);
        }),
      ]);
    } finally {
      if (timeoutHandle) {
        clearTimeout(timeoutHandle);
      }
    }
  }

  async complete(): Promise<void> {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.pendingWrites = this.pendingWrites.then(
      () =>
        new Promise<void>((resolve, reject) => {
          this.call.write(
            {
              conversation_id: this.conversationIdBytes,
              complete: true,
            },
            (error: any) => {
              if (error) {
                reject(error);
                return;
              }
              resolve();
            },
          );
        }),
    );
    const writesFinished = await this.awaitWithTimeout(
      this.pendingWrites,
      RESPONSE_RECORDER_COMPLETE_TIMEOUT_MS,
    );
    if (writesFinished === null) {
      this.call.cancel?.();
      return;
    }
    this.call.end();
    const finished = await this.awaitWithTimeout(
      this.responseDone,
      RESPONSE_RECORDER_COMPLETE_TIMEOUT_MS,
    );
    if (finished === null) {
      this.call.cancel?.();
    }
  }
}

function toModelMessage(entry: MemoryEntry): ModelMessage {
  return {
    role: entry.role === "AI" ? "assistant" : "user",
    content: entry.text,
  };
}

class BufferedContextMemory {
  private modified = false;
  private flushMode: "append" | "sync" = "append";
  private readonly messages: ModelMessage[] = [];
  private readonly appendedMessages: ModelMessage[] = [];

  constructor(
    private readonly options: MemoryServiceProxyOptions & {
      conversationId: string;
      memoryContentType: string;
    },
  ) {}

  async preload(): Promise<void> {
    const loadedMessages = await loadMessagesFromChannel({
      conversationId: this.options.conversationId,
      channel: "memory",
      authorization: this.options.authorization,
      baseUrl: this.options.baseUrl,
      unixSocket: this.options.unixSocket,
      apiKey: this.options.apiKey,
      memoryContentType: this.options.memoryContentType,
    });
    this.messages.push(...loadedMessages);
  }

  get(): ModelMessage[] {
    return this.messages.map((message) => ({ ...message }));
  }

  append(message: ModelMessage): void {
    this.messages.push(message);
    this.appendedMessages.push(message);
    this.modified = true;
  }

  clear(): void {
    this.messages.length = 0;
    this.appendedMessages.length = 0;
    this.modified = true;
    this.flushMode = "sync";
  }

  set(entries: ModelMessage[]): void {
    this.messages.length = 0;
    this.messages.push(...entries);
    this.appendedMessages.length = 0;
    this.modified = true;
    this.flushMode = "sync";
  }

  async flush(): Promise<void> {
    if (!this.modified) {
      return;
    }
    if (this.flushMode === "append") {
      if (this.appendedMessages.length === 0) {
        return;
      }
      await appendMemoryMessagesChunked({
        conversationId: this.options.conversationId,
        authorization: this.options.authorization,
        baseUrl: this.options.baseUrl,
        unixSocket: this.options.unixSocket,
        apiKey: this.options.apiKey,
        memoryContentType: this.options.memoryContentType,
        messages: this.appendedMessages,
      });
      return;
    }
    await syncMemoryMessages({
      conversationId: this.options.conversationId,
      authorization: this.options.authorization,
      baseUrl: this.options.baseUrl,
      unixSocket: this.options.unixSocket,
      apiKey: this.options.apiKey,
      memoryContentType: this.options.memoryContentType,
      messages: this.messages,
    });
  }
}

async function withContextMemory<T>(
  options: MemoryServiceProxyOptions & {
    conversationId: string;
    memoryContentType: string;
  },
  callback: (contextMemory: ContextMemory) => Promise<T>,
): Promise<T> {
  if (!options.memoryContentType?.trim()) {
    throw new Error("withContextMemory requires memoryContentType");
  }
  const buffer = new BufferedContextMemory(options);
  await buffer.preload();
  const result = await callback({
    get: () => buffer.get(),
    append: (entry) => buffer.append(entry),
    clear: () => buffer.clear(),
    set: (entries) => buffer.set(entries),
  });
  await buffer.flush();
  return result;
}

function coalesceHistoryEvents(events: HistoryEvent[]): HistoryEvent[] {
  const output: HistoryEvent[] = [];
  let pendingType: "PartialResponse" | "PartialThinking" | null = null;
  let pendingChunk = "";
  let pendingChunkField: "chunk" | "text" = "chunk";

  const flushPending = () => {
    if (!pendingType) {
      return;
    }
    output.push({
      eventType: pendingType,
      [pendingChunkField]: pendingChunk,
    });
    pendingType = null;
    pendingChunk = "";
    pendingChunkField = "chunk";
  };

  for (const event of events) {
    const eventChunk =
      typeof event.chunk === "string"
        ? event.chunk
        : typeof event.text === "string"
          ? event.text
          : null;
    if (
      (event.eventType === "PartialResponse" ||
        event.eventType === "PartialThinking") &&
      eventChunk !== null
    ) {
      if (pendingType === event.eventType) {
        pendingChunk += eventChunk;
      } else {
        flushPending();
        pendingType = event.eventType;
        pendingChunk = eventChunk;
        pendingChunkField = typeof event.chunk === "string" ? "chunk" : "text";
      }
      continue;
    }
    flushPending();
    output.push(event);
  }
  flushPending();
  return output;
}

function extractFinalTextFromHistoryEvents(events: HistoryEvent[]): string {
  let answer = "";
  for (const event of events) {
    if (event.eventType !== "PartialResponse") {
      continue;
    }
    if (typeof event.chunk === "string") {
      answer += event.chunk;
      continue;
    }
    if (typeof event.text === "string") {
      answer += event.text;
    }
  }
  return answer;
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  if (typeof error === "string") {
    return error;
  }
  try {
    return JSON.stringify(error);
  } catch {
    return "unknown error";
  }
}

function mapVercelAiChunkToHistoryEvent(
  chunk: VercelAiChunk,
): HistoryEvent | null {
  const type = typeof chunk.type === "string" ? chunk.type : "";
  if (type === "text-delta" && typeof chunk.text === "string") {
    return { eventType: "PartialResponse", chunk: chunk.text };
  }
  if (type === "reasoning-delta" && typeof chunk.text === "string") {
    return { eventType: "PartialThinking", chunk: chunk.text };
  }
  if (type === "tool-call") {
    return {
      eventType: "BeforeToolExecution",
      id: chunk.toolCallId ?? chunk.id ?? null,
      toolName: chunk.toolName ?? null,
      input: chunk.input ?? chunk.args ?? chunk.arguments ?? null,
    };
  }
  if (type === "tool-result") {
    return {
      eventType: "ToolExecuted",
      id: chunk.toolCallId ?? chunk.id ?? null,
      toolName: chunk.toolName ?? null,
      output: chunk.output ?? chunk.result ?? null,
    };
  }
  if (type === "finish-step") {
    return {
      eventType: "IntermediateResponse",
      finishReason: chunk.finishReason ?? null,
      usage: chunk.usage ?? null,
      response: chunk.response ?? null,
    };
  }
  if (type === "finish") {
    return {
      eventType: "ChatCompleted",
      finishReason: chunk.finishReason ?? null,
      usage: chunk.totalUsage ?? null,
    };
  }
  if (type === "error") {
    return {
      eventType: "StreamError",
      error: toErrorMessage(chunk.error),
    };
  }
  return null;
}

function resolveAssistantText(
  result: unknown,
  historyEvents: HistoryEvent[],
  recordedText: string,
): string {
  if (recordedText.length > 0) {
    return recordedText;
  }
  if (typeof result === "string") {
    return result;
  }
  if (
    typeof result === "object" &&
    result !== null &&
    "text" in result &&
    typeof (result as { text: unknown }).text === "string"
  ) {
    return (result as { text: string }).text;
  }
  const fromEvents = extractFinalTextFromHistoryEvents(historyEvents);
  return fromEvents;
}

export async function withMemoryService<T>(
  options: MemoryServiceProxyOptions &
    AppendEntriesBaseOptions & {
      conversationId: string;
      memoryContentType: string;
      historyContentType?: string;
      userText?: string | null;
      indexer?: ((message: IndexedMessage) => string | null | undefined) | null;
    },
  callback: (
    contextMemory: ContextMemory,
    responseRecorder: WithMemoryServiceResponseRecorder,
  ) => Promise<T>,
): Promise<T> {
  const indexer = options.indexer ?? null;
  const userText = options.userText;
  const shouldRecordHistory = typeof userText === "string";
  const historyContentType = options.historyContentType ?? "history/vercelai";
  const historyEvents: HistoryEvent[] = [];
  let recordedText = "";
  const hasGrpc = await hasGrpcDependencies();
  const responseState = { canceled: false };
  activeResponseStates.set(options.conversationId, responseState);
  const grpcRecorder = hasGrpc
    ? await (async () => {
        try {
          return new GrpcConversationRecorder(
            await createResponseRecorderClient(),
            options.conversationId,
          );
        } catch {
          return null;
        }
      })()
    : null;

  const recordChunk = (chunk: string) => {
    if (responseState.canceled) {
      throw new Error("response recording canceled");
    }
    recordedText += chunk;
    grpcRecorder?.record(chunk);
  };

  const recordHistoryEvent = (event: HistoryEvent) => {
    historyEvents.push(event);
  };

  const recordHistoryEvents = (events: HistoryEvent[]) => {
    for (const event of events) {
      recordHistoryEvent(event);
    }
  };

  const recordStream = async function* <T>(
    stream: AsyncIterable<T>,
    mapper?: (item: T) => HistoryEvent | null,
  ): AsyncIterable<HistoryEvent> {
    for await (const item of stream) {
      if (responseState.canceled) {
        break;
      }
      const event =
        mapper?.(item) ??
        (typeof item === "string"
          ? { eventType: "PartialResponse", chunk: item }
          : mapVercelAiChunkToHistoryEvent(item as VercelAiChunk));
      if (!event) {
        continue;
      }
      recordHistoryEvent(event);

      const eventText =
        typeof event.chunk === "string"
          ? event.chunk
          : typeof event.text === "string" &&
              event.eventType === "PartialResponse"
            ? event.text
            : null;
      if (eventText !== null) {
        if (mapper) {
          recordChunk(`${JSON.stringify(event)}\n`);
        } else if (typeof item === "string") {
          recordChunk(eventText);
        } else {
          recordChunk(`${JSON.stringify(event)}\n`);
        }
      } else {
        recordChunk(`${JSON.stringify(event)}\n`);
      }
      yield event;
    }
  };

  const record: WithMemoryServiceResponseRecorder["record"] = ((
    value: string | AsyncIterable<unknown>,
    mapper?: (item: unknown) => HistoryEvent | null,
  ) => {
    if (typeof value === "string") {
      recordChunk(value);
      return;
    }
    return recordStream(value, mapper);
  }) as WithMemoryServiceResponseRecorder["record"];

  const responseRecorder: WithMemoryServiceResponseRecorder = {
    record,
    text: () => recordedText,
    isCanceled: () => responseState.canceled,
    recordHistoryEvent,
    recordHistoryEvents,
    createVercelAiEventListeners: () => ({
      onChunk: ({ chunk }) => {
        const event = mapVercelAiChunkToHistoryEvent(chunk);
        if (event) {
          historyEvents.push(event);
        }
      },
      onError: ({ error }) => {
        historyEvents.push({
          eventType: "StreamError",
          error: toErrorMessage(error),
        });
      },
      onAbort: () => {
        historyEvents.push({ eventType: "StreamAborted" });
      },
      onStepFinish: (event) => {
        historyEvents.push({
          eventType: "StepCompleted",
          ...event,
        });
      },
      onFinish: (event) => {
        historyEvents.push({
          eventType: "ChatCompleted",
          ...event,
        });
      },
    }),
  };

  if (shouldRecordHistory) {
    await appendHistoryEntry({
      conversationId: options.conversationId,
      authorization: options.authorization,
      baseUrl: options.baseUrl,
      apiKey: options.apiKey,
      unixSocket: options.unixSocket,
      role: "USER",
      text: userText,
      indexedContent:
        indexer?.({
          role: "USER",
          text: userText,
        }) ?? undefined,
      forkedAtConversationId: options.forkedAtConversationId ?? undefined,
      forkedAtEntryId: options.forkedAtEntryId ?? undefined,
    });
  }

  let result: T;
  try {
    result = await withContextMemory(
      {
        conversationId: options.conversationId,
        authorization: options.authorization,
        baseUrl: options.baseUrl,
        apiKey: options.apiKey,
        unixSocket: options.unixSocket,
        memoryContentType: options.memoryContentType,
      },
      (contextMemory) => callback(contextMemory, responseRecorder),
    );
  } finally {
    await grpcRecorder?.complete();
    activeResponseStates.delete(options.conversationId);
  }

  if (shouldRecordHistory) {
    const coalescedHistoryEvents = coalesceHistoryEvents(historyEvents);
    const assistantText = resolveAssistantText(
      result,
      coalescedHistoryEvents,
      recordedText,
    );
    if (coalescedHistoryEvents.length > 0) {
      await appendHistoryEventsEntry({
        conversationId: options.conversationId,
        authorization: options.authorization,
        baseUrl: options.baseUrl,
        apiKey: options.apiKey,
        unixSocket: options.unixSocket,
        role: "AI",
        text: assistantText,
        events: coalescedHistoryEvents,
        historyContentType,
        indexedContent:
          indexer?.({
            role: "AI",
            text: assistantText,
          }) ?? undefined,
      });
    } else {
      await appendHistoryEntry({
        conversationId: options.conversationId,
        authorization: options.authorization,
        baseUrl: options.baseUrl,
        apiKey: options.apiKey,
        unixSocket: options.unixSocket,
        role: "AI",
        text: assistantText,
        indexedContent:
          indexer?.({
            role: "AI",
            text: assistantText,
          }) ?? undefined,
      });
    }
  }

  return result;
}

async function loadMessagesFromChannel(
  options: MemoryServiceProxyOptions & {
    conversationId: string;
    channel: "history" | "memory";
    forks?: "all" | null;
    memoryContentType?: string;
  },
): Promise<ModelMessage[]> {
  const messages: ModelMessage[] = [];
  let afterCursor: string | null = null;
  for (;;) {
    const query = compactQuery({
      channel: options.channel,
      forks: options.forks ?? undefined,
      limit: 200,
      afterCursor,
    });
    const response = await memoryServiceRequest(
      "GET",
      `/v1/conversations/${options.conversationId}/entries${query}`,
      options,
    );
    if (!response.ok) {
      return messages;
    }
    const payload = (await response.json()) as {
      data?: Array<{
        contentType?: string;
        content?: Array<
          | ModelMessage
          | {
              role?: string;
              text?: string;
            }
        >;
      }>;
      afterCursor?: string | null;
    };
    for (const entry of payload.data ?? []) {
      if (
        options.memoryContentType &&
        entry.contentType === options.memoryContentType &&
        entry.content
      ) {
        messages.push(...(entry.content as ModelMessage[]));
        continue;
      }
      for (const item of entry.content ?? []) {
        if (!("text" in item) || !item.text) continue;
        messages.push({
          role: item.role === "AI" ? "assistant" : "user",
          content: item.text,
        });
      }
    }
    if (!payload.afterCursor) {
      return messages;
    }
    afterCursor = payload.afterCursor;
  }
}

const MAX_RESPONSE_RECORDER_REDIRECTS = 5;

async function replayFromTarget(
  conversationId: string,
  target?: string,
  redirects = 0,
): Promise<string[]> {
  if (redirects > MAX_RESPONSE_RECORDER_REDIRECTS) {
    throw new Error("too many response recorder redirects");
  }
  const client = await createResponseRecorderClient(target);
  return new Promise<string[]>((resolve, reject) => {
    const chunks: string[] = [];
    let redirectAddress: string | null = null;
    const call = client.Replay({
      conversation_id: uuidToBytes(conversationId),
    });
    call.on("data", (message: any) => {
      if (
        typeof message.redirect_address === "string" &&
        message.redirect_address
      ) {
        redirectAddress = message.redirect_address;
        return;
      }
      if (typeof message.content === "string" && message.content) {
        chunks.push(message.content);
      }
    });
    call.on("error", reject);
    call.on("end", async () => {
      if (redirectAddress) {
        try {
          resolve(
            await replayFromTarget(
              conversationId,
              redirectAddress,
              redirects + 1,
            ),
          );
        } catch (error) {
          reject(error);
        }
        return;
      }
      resolve(chunks);
    });
  });
}

async function* replayChunksAsEvents(chunks: string[]): AsyncIterable<unknown> {
  let lineBuffer = "";
  for (const chunk of chunks) {
    lineBuffer += chunk;
    let newlineIndex = lineBuffer.indexOf("\n");
    if (newlineIndex < 0) {
      continue;
    }
    while (newlineIndex >= 0) {
      const line = lineBuffer.slice(0, newlineIndex).trim();
      lineBuffer = lineBuffer.slice(newlineIndex + 1);
      if (line) {
        try {
          yield JSON.parse(line) as unknown;
        } catch {
          // Keep original payload when it is not valid JSON.
          yield line;
        }
      }
      newlineIndex = lineBuffer.indexOf("\n");
    }
  }
  if (lineBuffer) {
    try {
      yield JSON.parse(lineBuffer) as unknown;
    } catch {
      yield lineBuffer;
    }
  }
}

async function cancelFromTarget(
  conversationId: string,
  target?: string,
  redirects = 0,
): Promise<void> {
  if (redirects > MAX_RESPONSE_RECORDER_REDIRECTS) {
    throw new Error("too many response recorder redirects");
  }
  const client = await createResponseRecorderClient(target);
  const response = await grpcUnary<any>(client, "Cancel", {
    conversation_id: uuidToBytes(conversationId),
  });
  if (
    typeof response?.redirect_address === "string" &&
    response.redirect_address
  ) {
    await cancelFromTarget(
      conversationId,
      response.redirect_address,
      redirects + 1,
    );
  }
}

export async function memoryServiceResumeCheck(
  conversationIds: string[],
): Promise<string[]> {
  if (!(await hasGrpcDependencies())) {
    return [];
  }
  const client = await createResponseRecorderClient();
  const response = await grpcUnary<any>(client, "CheckRecordings", {
    conversation_ids: conversationIds.map(uuidToBytes),
  });
  return (response?.conversation_ids ?? []).map((value: Buffer | Uint8Array) =>
    bytesToUuid(value),
  );
}

export async function* memoryServiceReplay(
  conversationId: string,
): AsyncIterable<unknown> {
  if (!(await hasGrpcDependencies())) {
    return;
  }
  const chunks = await replayFromTarget(conversationId);
  yield* replayChunksAsEvents(chunks);
}

export async function memoryServiceCancel(
  conversationId: string,
): Promise<void> {
  const active = activeResponseStates.get(conversationId);
  if (active) {
    active.canceled = true;
  }
  if (!(await hasGrpcDependencies())) {
    return;
  }
  await cancelFromTarget(conversationId);
}
