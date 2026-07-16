import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";

interface LoadMoreButtonProps {
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onLoadMore: () => void;
}

/**
 * Load more button component for paginated search results.
 * Displays a button to fetch the next page of results, with loading state.
 * Automatically hides when there are no more pages to load.
 */
export function LoadMoreButton({
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
}: LoadMoreButtonProps) {
  if (!hasNextPage) {
    return null;
  }

  return (
    <div className="flex justify-center">
      <button
        onClick={onLoadMore}
        disabled={isFetchingNextPage}
        className={cn(
          "rounded-lg px-4 py-2 text-sm font-medium transition-colors",
          isFetchingNextPage
            ? "cursor-not-allowed bg-sage-soft/30 text-muted-foreground"
            : "bg-sage-soft text-foreground hover:bg-sage-soft/80"
        )}
      >
        {isFetchingNextPage ? (
          <>
            <Loader2 className="mr-2 inline h-4 w-4 animate-spin" />
            Loading...
          </>
        ) : (
          "Load More"
        )}
      </button>
    </div>
  );
}
