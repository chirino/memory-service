import * as React from "react";
import { Copy, Check } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  useCopyToClipboard,
  type UseCopyToClipboardOptions,
} from "@/hooks/useCopyToClipboard";

export interface CopyButtonProps
  extends Omit<
      React.ButtonHTMLAttributes<HTMLButtonElement>,
      "children" | "onError"
    >,
    UseCopyToClipboardOptions {
  /** The text to copy to clipboard */
  value: string;
  /** Size of the icon (default: 4 = w-4 h-4) */
  iconSize?: 3 | 3.5 | 4 | 5;
}

/**
 * A button that copies text to clipboard with visual feedback.
 *
 * Shows a Copy icon by default, switches to a green Check icon temporarily
 * after successful copy.
 *
 * @example
 * ```tsx
 * <CopyButton value={conversationId} />
 * ```
 *
 * @example With custom size
 * ```tsx
 * <CopyButton value={uuid} iconSize={3.5} />
 * ```
 */
const CopyButton = React.forwardRef<HTMLButtonElement, CopyButtonProps>(
  (
    { value, iconSize = 4, timeout, onSuccess, onError, className, ...props },
    ref,
  ) => {
    const { copied, copy } = useCopyToClipboard({
      timeout,
      onSuccess,
      onError,
    });

    const sizeClass = {
      3: "w-3 h-3",
      3.5: "w-3.5 h-3.5",
      4: "w-4 h-4",
      5: "w-5 h-5",
    }[iconSize];

    return (
      <button
        ref={ref}
        type="button"
        onClick={() => copy(value)}
        className={cn(
          "rounded-md p-1 transition-colors",
          copied
            ? "text-primary"
            : "text-muted-foreground hover:bg-white/75 hover:text-foreground",
          className,
        )}
        title={copied ? "Copied!" : "Copy to clipboard"}
        {...props}
      >
        {copied ? (
          <Check className={sizeClass} />
        ) : (
          <Copy className={sizeClass} />
        )}
      </button>
    );
  },
);
CopyButton.displayName = "CopyButton";

export { CopyButton };

// Made with Bob
