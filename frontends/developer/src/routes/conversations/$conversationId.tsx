import * as React from "react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { GitFork, Loader2, Network } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { CopyButton } from "@/components/ui/copy-button";
import { ForkPointBadge } from "@/components/ui/fork-point-badge";
import { TimestampPopover } from "@/components/ui/timestamp-popover";
import {
  ContentRenderer,
  getLlmContextEntries,
  LlmContextRenderer,
  type ViewMode,
} from "@/components/content-renderers";
import { cn } from "@/lib/utils";
import { buildRenderItems, filterRenderItemsByChannel, type ChannelFilter } from "@/lib/entry-render-items";
import { buildForkNavigationSearch } from "@/lib/fork-navigation";
import { useScrollToEntry, useLineageEntries, type EntryWithForkPoint } from "@/hooks";
import {
  adminGetConversationOptions,
  adminGetMembershipsOptions,
  adminListForksOptions,
  adminListChildConversationsOptions,
  type ConversationMembership,
} from "@/api/client";

const validChannels = ["all", "history", "context", "journal"] as const;
type ChannelParam = (typeof validChannels)[number];

export const Route = createFileRoute("/conversations/$conversationId")({
  component: ConversationDetailPage,
  validateSearch: (search: Record<string, unknown>) => {
    const channel =
      typeof search.channel === "string" && validChannels.includes(search.channel as ChannelParam)
        ? (search.channel as ChannelParam)
        : undefined;
    return {
      entryId: typeof search.entryId === "string" ? search.entryId : undefined,
      forkedAt: typeof search.forkedAt === "string" ? search.forkedAt : undefined,
      channel,
    } as { entryId?: string; forkedAt?: string; channel?: ChannelParam };
  },
});

type TabType = "entries" | "memberships";
type ContextGroupView = "entries" | "resolved";

// Fork tree item type (for UI display)
interface ForkTreeItem {
  id: string;
  title: string;
  isRoot: boolean;
  forkedAtEntryId?: string;
  parentConversationId?: string;
  updatedAt: string;
}

function ConversationDetailPage() {
  const { conversationId } = Route.useParams();
  const { entryId: targetEntryId, forkedAt: forkedAtEntryId, channel: urlChannel } = Route.useSearch();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = React.useState<TabType>("entries");
  const [contextView, setContextView] = React.useState<ContextGroupView>("resolved");
  const [historyViewMode, setHistoryViewMode] = React.useState<ViewMode>("rendered");
  const channelFilter: ChannelFilter = urlChannel || "history";

  // Update channel filter in URL (preserves other params)
  const setChannelFilter = React.useCallback(
    (channel: ChannelFilter | string) => {
      navigate({
        to: ".",
        search: (prev) => ({
          ...prev,
          channel: channel === "history" ? undefined : (channel as ChannelParam),
        }),
        replace: true,
      });
    },
    [navigate],
  );

  // Fetch conversation data from API
  const {
    data: conversation,
    isLoading: conversationLoading,
    error: conversationError,
  } = useQuery(adminGetConversationOptions({ path: { id: conversationId } }));

  const { data: membershipsData, isLoading: membershipsLoading } = useQuery(
    adminGetMembershipsOptions({ path: { id: conversationId } }),
  );

  const { data: forksData, isLoading: forksLoading } = useQuery(
    adminListForksOptions({ path: { id: conversationId } }),
  );

  const { data: childrenData, isLoading: childrenLoading } = useQuery(
    adminListChildConversationsOptions({ path: { id: conversationId } }),
  );

  // Fetch the newest selected-ancestry page, then page backward; sibling entries stay unloaded.
  const {
    entries: allEntries,
    isLoading: entriesLoading,
    hasOlderEntries,
    isLoadingOlderEntries,
    loadOlderEntries,
  } = useLineageEntries({
    conversationId,
    forkPoints: forksData?.forkPoints || [],
  });

  const memberships = membershipsData?.data || [];

  // Scroll to entry from search results or fork navigation
  const { highlightedEntryId, getEntryRef, selectWithoutScroll } = useScrollToEntry({
    targetEntryId,
    forkedAtEntryId,
    entries: allEntries,
    setChannelFilter,
  });
  const renderItems = React.useMemo(
    () => filterRenderItemsByChannel(buildRenderItems(allEntries), channelFilter),
    [allEntries, channelFilter],
  );

  // Select an entry and update URL for sharing (preserves other params)
  // Uses selectWithoutScroll to prevent autoscrolling when clicking entries on the page
  // Uses resetScroll: false to prevent TanStack Router's default scroll-to-top
  const selectEntry = React.useCallback(
    (entryId: string) => {
      selectWithoutScroll(entryId);
      navigate({
        to: ".",
        search: (prev) => ({
          ...prev,
          entryId,
          forkedAt: undefined, // Clear forkedAt when selecting a specific entry
        }),
        replace: false,
        resetScroll: false,
      });
    },
    [navigate, selectWithoutScroll],
  );

  // Transform the complete navigation snapshot to the sidebar's flat group list.
  const forkTree: ForkTreeItem[] = React.useMemo(() => {
    const conversationIds = forksData?.conversationIds || [];
    if (conversationIds.length === 0 && conversation) {
      // Fallback if no forks returned
      return [
        {
          id: conversationId,
          title: conversation.title || "This conversation",
          isRoot: true,
          updatedAt: conversation.updatedAt || new Date().toISOString(),
        },
      ];
    }
    const optionsByConversation = new Map(
      (forksData?.forkPoints || []).flatMap((point) =>
        point.options.map((option) => [option.conversationId, option] as const),
      ),
    );
    return conversationIds.map((id, index) => {
      const option = optionsByConversation.get(id);
      return {
        id,
        title: option?.title || (id === conversationId ? conversation?.title : undefined) || "Untitled",
        isRoot: index === 0,
        forkedAtEntryId: option?.entryId,
        updatedAt: option?.createdAt || conversation?.updatedAt || new Date().toISOString(),
      };
    });
  }, [forksData?.conversationIds, forksData?.forkPoints, conversation, conversationId]);

  const childConversations = childrenData?.data || [];

  const isLoading = conversationLoading || entriesLoading || membershipsLoading || forksLoading || childrenLoading;

  // Loading state
  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="console-panel rounded-2xl p-10 text-center">
          <Loader2 className="mx-auto mb-4 h-8 w-8 animate-spin text-muted-foreground" />
          <p className="text-sm text-muted-foreground">Loading conversation...</p>
        </div>
      </div>
    );
  }

  // Error state
  if (conversationError) {
    return (
      <div className="p-8">
        <div className="console-panel rounded-2xl p-8 text-center">
          <p className="font-medium text-destructive">Failed to load conversation</p>
          <p className="mt-2 text-sm text-muted-foreground">
            {conversationError instanceof Error ? conversationError.message : "Unknown error"}
          </p>
          <Link to="/conversations" className="mt-4 inline-block">
            <button className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90">
              Back to Conversations
            </button>
          </Link>
        </div>
      </div>
    );
  }

  // Format date
  const formatDate = (dateString: string) => {
    const date = new Date(dateString);
    return date.toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      year: "numeric",
      hour: "numeric",
      minute: "2-digit",
      hour12: true,
    });
  };

  const formatShortDate = (dateString: string) => {
    const date = new Date(dateString);
    return date.toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      year: "numeric",
    });
  };

  if (!conversation) {
    return (
      <div className="p-8">
        <div className="console-panel rounded-2xl p-8 text-center text-muted-foreground">
          Conversation with ID {conversationId} was not found.
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col gap-8 overflow-hidden px-5 py-8 md:flex-row md:px-10">
      <div className="min-w-0 flex-1 overflow-auto pr-2">
        <nav className="mb-5">
          <ol className="flex items-center gap-2 text-sm">
            <li>
              <Link to="/conversations" className="text-muted-foreground transition-colors hover:text-foreground">
                Conversations
              </Link>
            </li>
            <span className="text-muted-foreground">/</span>
            <li className="font-medium text-foreground">{conversation.title || "Untitled"}</li>
          </ol>
        </nav>

        <div className="mb-7 flex items-start justify-between gap-6">
          <div>
            <h1 className="console-title mb-3 text-3xl leading-tight text-foreground md:text-4xl">
              {conversation.title || "Untitled"}
            </h1>
            <div className="flex flex-wrap items-center gap-4 text-sm text-muted-foreground">
              <div className="flex items-center gap-1.5">
                <code className="console-code px-2 py-1 font-mono text-xs">{conversationId}</code>
                <CopyButton value={conversationId} iconSize={3.5} />
              </div>
              <span>Owner: {conversation.ownerUserId}</span>
              {conversation.clientId && (
                <span>
                  Client: <code className="font-mono">{conversation.clientId}</code>
                </span>
              )}
              {conversation.agentId && (
                <span>
                  Agent: <code className="font-mono">{conversation.agentId}</code>
                </span>
              )}
            </div>
            {conversation.startedByConversationId && (
              <div className="mt-1 flex items-center gap-2 text-sm text-muted-foreground">
                <span>Child of</span>
                <Link
                  to="/conversations/$conversationId"
                  params={{ conversationId: conversation.startedByConversationId }}
                  search={conversation.startedByEntryId ? { entryId: conversation.startedByEntryId } : undefined}
                  className="font-mono text-xs text-primary hover:underline"
                >
                  {conversation.startedByConversationId.slice(0, 12)}...
                </Link>
                {conversation.startedByEntryId && (
                  <>
                    <span>at entry</span>
                    <Link
                      to="/conversations/$conversationId"
                      params={{ conversationId: conversation.startedByConversationId }}
                      search={{ entryId: conversation.startedByEntryId }}
                      className="font-mono text-xs text-primary hover:underline"
                    >
                      {conversation.startedByEntryId.slice(0, 12)}...
                    </Link>
                  </>
                )}
              </div>
            )}
          </div>
          <div className="hidden text-right text-sm leading-7 text-muted-foreground lg:block">
            <div>Created: {formatDate(conversation.createdAt || "")}</div>
            <div>Updated: {formatDate(conversation.updatedAt || "")}</div>
          </div>
        </div>

        <div className="mb-5">
          <nav className="console-segmented">
            <button
              onClick={() => setActiveTab("entries")}
              className={cn("console-segment", activeTab === "entries" && "console-segment-active")}
            >
              Entries
            </button>
            <button
              onClick={() => setActiveTab("memberships")}
              className={cn("console-segment", activeTab === "memberships" && "console-segment-active")}
            >
              Memberships
            </button>
          </nav>
        </div>

        {/* Tab Content */}
        {activeTab === "entries" ? (
          <div>
            {/* Entry controls */}
            <div className="mb-5 flex flex-wrap items-end gap-4">
              <div>
                <div className="mb-1.5 text-xs font-medium text-muted-foreground">Channel</div>
                <div className="console-segmented">
                  {(["history", "all", "context", "journal"] as ChannelFilter[]).map((channel) => (
                    <button
                      key={channel}
                      onClick={() => setChannelFilter(channel)}
                      className={cn(
                        "console-segment min-w-0 px-4",
                        channelFilter === channel && "console-segment-active",
                      )}
                    >
                      {channel.charAt(0).toUpperCase() + channel.slice(1)}
                    </button>
                  ))}
                </div>
              </div>
              {(channelFilter === "history" || channelFilter === "all") && (
                <div>
                  <div className="mb-1.5 text-xs font-medium text-muted-foreground">History view</div>
                  <div className="console-segmented">
                    {(
                      [
                        ["rendered", "Rendered"],
                        ["raw", "JSON"],
                      ] as const
                    ).map(([mode, label]) => (
                      <button
                        key={mode}
                        type="button"
                        onClick={() => setHistoryViewMode(mode)}
                        className={cn(
                          "console-segment min-w-0 px-4",
                          historyViewMode === mode && "console-segment-active",
                        )}
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                </div>
              )}
              {(channelFilter === "context" || channelFilter === "all") && (
                <div>
                  <div className="mb-1.5 text-xs font-medium text-muted-foreground">Context view</div>
                  <div className="console-segmented">
                    {(
                      [
                        ["resolved", "LLM Context"],
                        ["entries", "Entries"],
                      ] as const
                    ).map(([view, label]) => (
                      <button
                        key={view}
                        type="button"
                        onClick={() => setContextView(view)}
                        className={cn("console-segment min-w-0 px-4", contextView === view && "console-segment-active")}
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>

            {/* Entries List */}
            <div className="space-y-4">
              {hasOlderEntries && (
                <div className="flex justify-center pb-1">
                  <button
                    type="button"
                    onClick={() => void loadOlderEntries()}
                    disabled={isLoadingOlderEntries}
                    className="console-panel rounded-lg px-4 py-2 text-sm text-muted-foreground transition-colors hover:text-foreground disabled:cursor-wait disabled:opacity-60"
                  >
                    {isLoadingOlderEntries ? "Loading older entries..." : "Load older entries"}
                  </button>
                </div>
              )}
              {renderItems.map((item, i) =>
                item.type === "entry" ? (
                  <EntryCard
                    key={item.entry.id}
                    entry={item.entry}
                    formatDate={formatDate}
                    isHighlighted={highlightedEntryId === item.entry.id}
                    ref={getEntryRef(item.entry.id)}
                    onClick={() => selectEntry(item.entry.id)}
                    channelFilter={channelFilter}
                    historyViewMode={historyViewMode}
                  />
                ) : (
                  <ContextGroup
                    key={`ctx-group-${i}`}
                    entries={item.entries}
                    allEntries={allEntries}
                    view={contextView}
                    formatDate={formatDate}
                    highlightedEntryId={highlightedEntryId}
                    getEntryRef={getEntryRef}
                    onEntryClick={selectEntry}
                    channelFilter={channelFilter}
                    historyViewMode={historyViewMode}
                  />
                ),
              )}
            </div>
          </div>
        ) : (
          <MembershipsTab memberships={memberships} />
        )}
      </div>

      <aside className="hidden w-80 flex-shrink-0 overflow-auto border-l border-[rgba(43,39,34,0.1)] pl-7 lg:block">
        <h3 className="mb-4 flex items-center gap-2 text-sm font-semibold text-foreground">
          <GitFork className="forks-icon h-4 w-4 text-primary" />
          <span>Forks</span>
        </h3>
        <div className="space-y-3">
          {forkTree.map((fork) => (
            <ForkCard
              key={fork.id}
              fork={fork}
              isActive={fork.id === conversationId}
              formatDate={formatShortDate}
              channelFilter={channelFilter}
            />
          ))}
        </div>
        <p className="mt-4 border-t border-[rgba(43,39,34,0.1)] pt-4 text-xs text-muted-foreground">
          {forkTree.length} conversation{forkTree.length !== 1 ? "s" : ""} in this fork tree
        </p>

        {/* Child Conversations (agent lineage) */}
        {childConversations.length > 0 && (
          <>
            <h3 className="mb-4 mt-6 flex items-center gap-2 text-sm font-semibold text-foreground">
              <Network className="h-4 w-4 text-primary" />
              <span>Child Conversations</span>
            </h3>
            <div className="space-y-3">
              {childConversations.map((child) => (
                <Link
                  key={child.id}
                  to="/conversations/$conversationId"
                  params={{ conversationId: child.id || "" }}
                  className="console-panel block rounded-xl p-3 transition-colors hover:bg-sage-soft/25"
                >
                  <div className="truncate text-sm font-medium text-foreground">{child.title || "Untitled"}</div>
                  <code className="mt-1 block truncate font-mono text-xs text-muted-foreground" title={child.id}>
                    {child.id}
                  </code>
                  {child.startedByEntryId && (
                    <div className="mt-1 text-xs text-muted-foreground">
                      Started at entry <span className="font-mono">{child.startedByEntryId.slice(0, 8)}...</span>
                    </div>
                  )}
                  <div className="mt-1 text-xs text-muted-foreground">{formatShortDate(child.createdAt || "")}</div>
                </Link>
              ))}
            </div>
            <p className="mt-4 border-t border-[rgba(43,39,34,0.1)] pt-4 text-xs text-muted-foreground">
              {childConversations.length} child conversation{childConversations.length !== 1 ? "s" : ""}
            </p>
          </>
        )}
      </aside>
    </div>
  );
}

// ─── Context Group ───────────────────────────────────────────────────────────

function ContextGroup({
  entries,
  allEntries,
  view,
  formatDate,
  highlightedEntryId,
  getEntryRef,
  onEntryClick,
  channelFilter,
  historyViewMode,
}: {
  entries: EntryWithForkPoint[];
  allEntries: EntryWithForkPoint[];
  view: ContextGroupView;
  formatDate: (date: string) => string;
  highlightedEntryId: string | null;
  getEntryRef: (entryId: string) => (el: HTMLElement | null) => void;
  onEntryClick: (id: string) => void;
  channelFilter?: ChannelFilter;
  historyViewMode: ViewMode;
}) {
  if (view === "resolved") {
    const lastEntry = entries[entries.length - 1];
    if (!lastEntry) {
      return null;
    }
    const compositionEntries = getLlmContextEntries(entries, allEntries);
    const compositionCount = compositionEntries.length;
    return (
      <EntryCard
        entry={lastEntry}
        formatDate={formatDate}
        isHighlighted={highlightedEntryId === lastEntry.id}
        ref={getEntryRef(lastEntry.id)}
        onClick={() => onEntryClick(lastEntry.id)}
        channelFilter={channelFilter}
        historyViewMode={historyViewMode}
        entryIdLabel={`composed of ${compositionCount} ${compositionCount === 1 ? "entry" : "entries"}`}
        entryIdTitle={compositionEntries.map((entry) => entry.id).join("\n")}
      >
        <LlmContextRenderer groupEntries={entries} allEntries={allEntries} />
      </EntryCard>
    );
  }

  return (
    <div className="space-y-3">
      {entries.map((entry) => (
        <EntryCard
          key={entry.id}
          entry={entry}
          formatDate={formatDate}
          isHighlighted={highlightedEntryId === entry.id}
          ref={getEntryRef(entry.id)}
          onClick={() => onEntryClick(entry.id)}
          channelFilter={channelFilter}
          historyViewMode={historyViewMode}
        />
      ))}
    </div>
  );
}

// ─── Entry Card ──────────────────────────────────────────────────────────────

// Entry Card Component
const EntryCard = React.forwardRef<
  HTMLDivElement,
  {
    entry: EntryWithForkPoint;
    formatDate: (date: string) => string;
    isHighlighted?: boolean;
    onClick?: () => void;
    channelFilter?: ChannelFilter;
    historyViewMode: ViewMode;
    entryIdLabel?: string;
    entryIdTitle?: string;
    children?: React.ReactNode;
  }
>(
  (
    { entry, formatDate, isHighlighted, onClick, channelFilter, historyViewMode, entryIdLabel, entryIdTitle, children },
    ref,
  ) => {
    const viewMode = entry.channel === "history" ? historyViewMode : "rendered";

    const getChannelColor = (channel: string) => {
      switch (channel) {
        case "history":
          return "bg-sage-soft text-primary";
        case "context":
          return "bg-[#eadccd] text-[#98613d]";
        case "journal":
          return "bg-[#e2e0eb] text-[#615a7a]";
        default:
          return "bg-secondary text-muted-foreground";
      }
    };

    return (
      <div
        ref={ref}
        onClick={onClick}
        className={cn(
          "console-panel cursor-pointer rounded-xl p-4 transition-all duration-300 hover:bg-sage-soft/20",
          entry.isForkPoint ? "ring-1 ring-primary/20" : "",
          isHighlighted && "border-primary ring-2 ring-primary ring-offset-2",
        )}
      >
        {/* Header */}
        <div className="mb-2 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Badge className={cn("text-xs font-medium", getChannelColor(entry.channel || ""))}>{entry.channel}</Badge>
            {entry.epoch !== undefined && (
              <Badge className="bg-[#eadccd] text-xs font-medium text-[#98613d]">epoch: {entry.epoch}</Badge>
            )}
            <div className="flex min-w-0 flex-1 items-center gap-1">
              <span className="truncate font-mono text-xs text-muted-foreground" title={entryIdTitle ?? entry.id}>
                {entryIdLabel ?? entry.id}
              </span>
              {!entryIdLabel && <CopyButton value={entry.id || ""} iconSize={3} className="shrink-0" />}
            </div>
            {entry.isForkPoint && <ForkPointBadge forksAtPoint={entry.forksAtPoint} channelFilter={channelFilter} />}
          </div>
          <div onClick={(e) => e.stopPropagation()}>
            <TimestampPopover timestamp={entry.createdAt || ""} displayText={formatDate(entry.createdAt || "")} />
          </div>
        </div>

        {/* User and content type */}
        <div className="mb-2 flex items-center justify-between text-sm text-muted-foreground">
          <div className="flex items-center gap-4">{entry.userId && <span>User: {entry.userId}</span>}</div>
          <div className="flex items-center gap-3">
            <span>Type: {entry.contentType}</span>
          </div>
        </div>

        {/* Content */}
        {children ?? (
          <ContentRenderer
            content={entry.content as unknown[]}
            contentType={entry.contentType || ""}
            viewMode={viewMode}
          />
        )}
      </div>
    );
  },
);
EntryCard.displayName = "EntryCard";

// Fork Card Component
function ForkCard({
  fork,
  isActive,
  formatDate,
  channelFilter,
}: {
  fork: ForkTreeItem;
  isActive: boolean;
  formatDate: (date: string) => string;
  channelFilter: ChannelFilter;
}) {
  const searchParams = buildForkNavigationSearch(fork.forkedAtEntryId, channelFilter);

  return (
    <Link
      to="/conversations/$conversationId"
      params={{ conversationId: fork.id }}
      search={Object.keys(searchParams).length > 0 ? searchParams : undefined}
      className={cn(
        "console-panel block rounded-xl p-3 transition-colors",
        isActive ? "fork-card-active bg-sage-soft/45 ring-1 ring-primary/20" : "hover:bg-sage-soft/25",
      )}
    >
      <code className="block truncate font-mono text-xs text-foreground" title={fork.id}>
        {fork.id}
      </code>
      {!fork.isRoot && fork.forkedAtEntryId && (
        <div className="mt-1 text-xs text-muted-foreground">
          Forked at{" "}
          <button onClick={(e) => e.preventDefault()} className="font-mono text-primary hover:underline">
            {fork.forkedAtEntryId.slice(0, 8)}...
          </button>
        </div>
      )}
      <div className="mt-1 text-xs text-muted-foreground">Updated {formatDate(fork.updatedAt)}</div>
    </Link>
  );
}

// Memberships Tab Component
function MembershipsTab({ memberships }: { memberships: ConversationMembership[] }) {
  const formatDate = (dateString: string) => {
    const date = new Date(dateString);
    return date.toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      year: "numeric",
    });
  };

  const getAccessLevelBadge = (level: string) => {
    switch (level) {
      case "owner":
        return "bg-sage-soft text-primary";
      case "writer":
        return "bg-[#e1ead8] text-[#5b6f43]";
      case "reader":
        return "bg-secondary text-secondary-foreground";
      default:
        return "bg-muted text-muted-foreground";
    }
  };

  if (memberships.length === 0) {
    return (
      <div className="console-panel rounded-xl p-8 text-center text-muted-foreground">
        No memberships found for this conversation.
      </div>
    );
  }

  return (
    <div className="overflow-hidden">
      <table className="console-table">
        <thead>
          <tr>
            <th>User ID</th>
            <th>Access Level</th>
            <th>Added</th>
          </tr>
        </thead>
        <tbody>
          {memberships.map((membership, idx) => (
            <tr key={idx}>
              <td>
                <code className="font-mono text-sm">{membership.userId}</code>
              </td>
              <td>
                <Badge className={cn("capitalize", getAccessLevelBadge(membership.accessLevel || ""))}>
                  {membership.accessLevel}
                </Badge>
              </td>
              <td className="text-sm text-muted-foreground">{formatDate(membership.createdAt || "")}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// Made with Bob
