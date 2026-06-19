/**
 * JSON Syntax Highlighting Component
 *
 * Lightweight CSS-based JSON syntax highlighter that tokenizes formatted JSON
 * and applies theme-aware color classes for keys, strings, numbers, booleans,
 * null values, and punctuation.
 */

function deepParseJson(value: unknown): unknown {
  if (typeof value !== "string") return value;
  try {
    const parsed = JSON.parse(value);
    // If the parsed result is still a string, try once more (double-encoded JSON)
    if (typeof parsed === "string") {
      try {
        return JSON.parse(parsed);
      } catch {
        return parsed;
      }
    }
    return parsed;
  } catch {
    return value;
  }
}

export function formatJson(value: unknown): string {
  try {
    const resolved = deepParseJson(value);
    if (typeof resolved === "string") return resolved;
    return JSON.stringify(resolved, null, 2);
  } catch {
    return String(value);
  }
}

/**
 * Renders a value as syntax-highlighted JSON.
 * Tokenizes the formatted JSON string and applies color classes
 * for keys, strings, numbers, booleans, and null values.
 */
export function JsonHighlight({
  value,
  className,
}: {
  value: unknown;
  className?: string;
}) {
  const text = formatJson(value);

  // Tokenize JSON string into typed segments for highlighting
  const tokens: {
    type: "key" | "string" | "number" | "boolean" | "null" | "punctuation";
    text: string;
  }[] = [];
  const tokenRegex =
    /("(?:[^"\\]|\\.)*")\s*:|("(?:[^"\\]|\\.)*")|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|(\btrue\b|\bfalse\b)|(\bnull\b)|([{}[\]:,])/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;

  while ((match = tokenRegex.exec(text)) !== null) {
    if (match.index > lastIndex) {
      tokens.push({
        type: "punctuation",
        text: text.slice(lastIndex, match.index),
      });
    }

    if (match[1] !== undefined) {
      tokens.push({ type: "key", text: match[1] });
      const colonIdx = text.indexOf(":", match.index + match[1].length);
      if (colonIdx >= 0) {
        tokens.push({
          type: "punctuation",
          text: text.slice(match.index + match[1].length, colonIdx + 1),
        });
        lastIndex = colonIdx + 1;
        continue;
      }
    } else if (match[2] !== undefined) {
      tokens.push({ type: "string", text: match[2] });
    } else if (match[3] !== undefined) {
      tokens.push({ type: "number", text: match[3] });
    } else if (match[4] !== undefined) {
      tokens.push({ type: "boolean", text: match[4] });
    } else if (match[5] !== undefined) {
      tokens.push({ type: "null", text: match[5] });
    } else if (match[6] !== undefined) {
      tokens.push({ type: "punctuation", text: match[6] });
    }
    lastIndex = match.index + match[0].length;
  }

  if (lastIndex < text.length) {
    tokens.push({ type: "punctuation", text: text.slice(lastIndex) });
  }

  if (tokens.length === 0) {
    return (
      <pre
        className={
          className ??
          "overflow-x-auto whitespace-pre-wrap rounded bg-muted/50 p-2 text-sm text-foreground font-mono"
        }
      >
        {text}
      </pre>
    );
  }

  const colorMap: Record<string, string> = {
    key: "text-syntax-key font-medium",
    string: "text-syntax-string",
    number: "text-syntax-number",
    boolean: "text-syntax-boolean font-medium",
    null: "text-syntax-null italic",
    punctuation: "text-syntax-punct",
  };

  return (
    <pre
      className={
        className ??
        "overflow-x-auto whitespace-pre-wrap rounded bg-muted/50 p-2 text-sm font-mono"
      }
    >
      {tokens.map((token, i) => (
        <span key={i} className={colorMap[token.type]}>
          {token.text}
        </span>
      ))}
    </pre>
  );
}

// Made with Bob