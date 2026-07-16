import { Loader2 } from "lucide-react";

interface SearchLoadingStateProps {
  message?: string;
}

/**
 * Loading state component displayed while a search query is being executed.
 * Shows a spinning loader icon and a customizable message.
 */
export function SearchLoadingState({ 
  message = "Searching..." 
}: SearchLoadingStateProps) {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="text-center">
        <Loader2 className="mx-auto mb-4 h-8 w-8 animate-spin text-primary" />
        <p className="text-sm text-muted-foreground">{message}</p>
      </div>
    </div>
  );
}
