interface NoResultsStateProps {
  query: string;
}

/**
 * No results state component displayed when a search query returns no matches.
 * Shows a message indicating that no results were found for the given query.
 */
export function NoResultsState({ query }: NoResultsStateProps) {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="text-center">
        <p className="text-muted-foreground">No results found for "{query}"</p>
      </div>
    </div>
  );
}
