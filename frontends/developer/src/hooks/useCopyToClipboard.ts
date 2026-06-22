import * as React from "react";

export interface UseCopyToClipboardOptions {
  /** Duration in ms before resetting copied state (default: 2000) */
  timeout?: number;
  /** Callback when copy succeeds */
  onSuccess?: () => void;
  /** Callback when copy fails */
  onError?: (error: Error) => void;
}

export interface UseCopyToClipboardReturn {
  /** Whether the text was recently copied */
  copied: boolean;
  /** Copy text to clipboard */
  copy: (text: string) => Promise<void>;
  /** Reset copied state manually */
  reset: () => void;
}

/**
 * Headless hook for copying text to clipboard with temporary "copied" state.
 *
 * @example
 * ```tsx
 * const { copied, copy } = useCopyToClipboard()
 *
 * return (
 *   <button onClick={() => copy(someText)}>
 *     {copied ? 'Copied!' : 'Copy'}
 *   </button>
 * )
 * ```
 */
export function useCopyToClipboard(
  options: UseCopyToClipboardOptions = {},
): UseCopyToClipboardReturn {
  const { timeout = 2000, onSuccess, onError } = options;
  const [copied, setCopied] = React.useState(false);
  const timeoutRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  const reset = React.useCallback(() => {
    setCopied(false);
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
  }, []);

  const copy = React.useCallback(
    async (text: string) => {
      try {
        await navigator.clipboard.writeText(text);
        setCopied(true);
        onSuccess?.();

        // Clear any existing timeout
        if (timeoutRef.current) {
          clearTimeout(timeoutRef.current);
        }

        // Reset after timeout
        timeoutRef.current = setTimeout(() => {
          setCopied(false);
          timeoutRef.current = null;
        }, timeout);
      } catch (error) {
        onError?.(error instanceof Error ? error : new Error("Failed to copy"));
      }
    },
    [timeout, onSuccess, onError],
  );

  // Cleanup on unmount
  React.useEffect(() => {
    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
      }
    };
  }, []);

  return { copied, copy, reset };
}

// Made with Bob