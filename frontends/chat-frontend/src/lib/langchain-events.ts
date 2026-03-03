import type { ChatEvent } from "@/components/conversation";

type JsonRecord = Record<string, unknown>;

function asRecord(value: unknown): JsonRecord | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  return value as JsonRecord;
}

function asString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function extractTextChunks(value: unknown): string[] {
  if (typeof value === "string") {
    return value ? [value] : [];
  }

  if (Array.isArray(value)) {
    const out: string[] = [];
    for (const item of value) {
      out.push(...extractTextChunks(item));
    }
    return out;
  }

  const record = asRecord(value);
  if (!record) {
    return [];
  }

  const direct = [record.text, record.content, record.value];
  for (const candidate of direct) {
    if (typeof candidate === "string" && candidate) {
      return [candidate];
    }
  }

  const blocks = record.content_blocks;
  if (Array.isArray(blocks)) {
    const out: string[] = [];
    for (const block of blocks) {
      out.push(...extractTextChunks(block));
    }
    if (out.length > 0) {
      return out;
    }
  }

  const delta = asRecord(record.delta);
  if (delta) {
    const out = extractTextChunks(delta);
    if (out.length > 0) {
      return out;
    }
  }

  return [];
}

function normalizeCanonicalEvent(eventRecord: JsonRecord): ChatEvent[] {
  const eventType = asString(eventRecord.eventType);
  if (!eventType) {
    return [];
  }

  switch (eventType) {
    case "PartialResponse": {
      const chunk = asString(eventRecord.chunk) ?? "";
      return chunk ? [{ eventType: "PartialResponse", chunk }] : [];
    }
    case "PartialThinking": {
      const chunk = asString(eventRecord.chunk) ?? "";
      return chunk ? [{ eventType: "PartialThinking", chunk }] : [];
    }
    case "IntermediateResponse": {
      const chunk = asString(eventRecord.chunk);
      return [{ eventType: "IntermediateResponse", chunk }];
    }
    case "BeforeToolExecution": {
      const toolName = asString(eventRecord.toolName) ?? "tool";
      const id = asString(eventRecord.id);
      return [{ eventType: "BeforeToolExecution", id, toolName, input: eventRecord.input }];
    }
    case "ToolExecuted": {
      const toolName = asString(eventRecord.toolName) ?? "tool";
      const id = asString(eventRecord.id);
      return [{ eventType: "ToolExecuted", id, toolName, input: eventRecord.input, output: eventRecord.output }];
    }
    case "ContentFetched": {
      const source = asString(eventRecord.source);
      const content = asString(eventRecord.content);
      return [{ eventType: "ContentFetched", source, content }];
    }
    case "ChatCompleted":
      return [{ eventType: "ChatCompleted", finishReason: asString(eventRecord.finishReason) }];
    default:
      return [];
  }
}

function normalizeLangChainEnvelope(eventRecord: JsonRecord): ChatEvent[] {
  const eventName = asString(eventRecord.event);
  if (!eventName) {
    return [];
  }

  const data = asRecord(eventRecord.data) ?? {};
  const name = asString(eventRecord.name) ?? asString(data.name) ?? "tool";
  const id = asString(eventRecord.run_id) ?? asString(eventRecord.id);

  switch (eventName) {
    case "on_tool_start":
      return [{ eventType: "BeforeToolExecution", id, toolName: name, input: data.input }];
    case "on_tool_end":
      return [{ eventType: "ToolExecuted", id, toolName: name, input: data.input, output: data.output }];
    case "on_chat_model_stream":
    case "on_llm_stream":
    case "on_chain_stream": {
      const chunks = extractTextChunks(data.chunk ?? data.output ?? data.text);
      return chunks.map((chunk) => ({ eventType: "PartialResponse", chunk }));
    }
    case "on_chain_end": {
      const chunks = extractTextChunks(data.output ?? data.text);
      return chunks.map((chunk) => ({ eventType: "IntermediateResponse", chunk }));
    }
    case "on_chat_model_end":
    case "on_llm_end":
      return [];
    default:
      return [];
  }
}

export function normalizeChatEvents(input: unknown): ChatEvent[] {
  if (Array.isArray(input)) {
    const out: ChatEvent[] = [];
    for (const item of input) {
      out.push(...normalizeChatEvents(item));
    }
    return out;
  }

  const record = asRecord(input);
  if (!record) {
    return [];
  }

  if (typeof record.eventType === "string") {
    return normalizeCanonicalEvent(record);
  }

  if (typeof record.event === "string") {
    return normalizeLangChainEnvelope(record);
  }

  return [];
}
