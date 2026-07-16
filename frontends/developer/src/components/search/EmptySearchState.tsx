import { Search as SearchIcon } from "lucide-react";

interface EmptySearchStateProps {
  message?: string;
}

/**
 * Empty state component displayed when no search query has been entered.
 * Shows a search icon and a message prompting the user to enter a query.
 */
export function EmptySearchState({ 
  message = "Enter a search query to get started" 
}: EmptySearchStateProps) {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="text-center">
        <SearchIcon className="mx-auto mb-4 h-12 w-12 text-primary/40" strokeWidth={1.45} />
        <p className="text-muted-foreground">{message}</p>
      </div>
    </div>
  );
}
