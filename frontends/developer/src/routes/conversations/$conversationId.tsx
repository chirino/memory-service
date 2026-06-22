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
	ViewToggle,
	useContentViewMode,
} from "@/components/content-renderers";
import { cn } from "@/lib/utils";
import { useScrollToEntry, useLineageEntries, type EntryWithForkPoint } from "@/hooks";
import {
	adminGetConversationOptions,
	adminGetMembershipsOptions,
	adminListForksOptions,
	adminListChildConversationsOptions,
	type ConversationMembership,
} from "@/api/client";

const validChannels = ["all", "history", "context"] as const;
type ChannelParam = (typeof validChannels)[number];

export const Route = createFileRoute("/conversations/$conversationId")({
	component: ConversationDetailPage,
	validateSearch: (search: Record<string, unknown>) => {
		const channel = typeof search.channel === "string" && validChannels.includes(search.channel as ChannelParam)
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
type ChannelFilter = "all" | "history" | "context";

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
	const channelFilter: ChannelFilter = urlChannel || "all";

	// Update channel filter in URL (preserves other params)
	const setChannelFilter = React.useCallback(
		(channel: ChannelFilter | string) => {
			navigate({
				to: ".",
				search: (prev) => ({
					...prev,
					channel: channel === "all" ? undefined : (channel as ChannelParam),
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
	} = useQuery(
		adminGetConversationOptions({ path: { id: conversationId } }),
	);

	const { data: membershipsData, isLoading: membershipsLoading } = useQuery(
		adminGetMembershipsOptions({ path: { id: conversationId } }),
	);

	const { data: forksData, isLoading: forksLoading } = useQuery(
		adminListForksOptions({ path: { id: conversationId } }),
	);

	const { data: childrenData, isLoading: childrenLoading } = useQuery(
		adminListChildConversationsOptions({ path: { id: conversationId } }),
	);

	// Fetch entries from the entire fork lineage (parent entries + current entries)
	const { entries: allEntries, isLoading: entriesLoading } = useLineageEntries({
		conversationId,
		forkSummaries: forksData?.data || [],
	});

	const memberships = membershipsData?.data || [];

	// Scroll to entry from search results or fork navigation
	const { highlightedEntryId, getEntryRef, selectWithoutScroll } = useScrollToEntry({
		targetEntryId,
		forkedAtEntryId,
		entries: allEntries,
		setChannelFilter,
	});

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

	// Transform API fork summaries to ForkTreeItem format for UI
	const forkTree: ForkTreeItem[] = React.useMemo(() => {
		const forks = forksData?.data || [];
		if (forks.length === 0 && conversation) {
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
		return forks.map((fork) => ({
			id: fork.conversationId || "",
			title: fork.title || "Untitled",
			isRoot: !fork.forkedAtEntryId,
			forkedAtEntryId: fork.forkedAtEntryId,
			parentConversationId: fork.forkedAtConversationId,
			updatedAt: fork.createdAt || new Date().toISOString(),
		}));
	}, [forksData?.data, conversation, conversationId]);

	const childConversations = childrenData?.data || [];

	const isLoading =
		conversationLoading || entriesLoading || membershipsLoading || forksLoading || childrenLoading;

	// Loading state
	if (isLoading) {
		return (
			<div className="flex h-full items-center justify-center">
				<div className="console-panel rounded-2xl p-10 text-center">
					<Loader2 className="w-8 h-8 animate-spin text-muted-foreground mx-auto mb-4" />
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
					<p className="text-destructive font-medium">
						Failed to load conversation
					</p>
					<p className="text-sm text-muted-foreground mt-2">
						{conversationError instanceof Error
							? conversationError.message
							: "Unknown error"}
					</p>
					<Link to="/conversations" className="inline-block mt-4">
						<button className="rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90">
							Back to Conversations
						</button>
					</Link>
				</div>
			</div>
		);
	}

	// Filter entries by channel
	const filteredEntries =
		channelFilter === "all"
			? allEntries
			: allEntries.filter((e) => e.channel === channelFilter);

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
							<Link
								to="/conversations"
								className="text-muted-foreground transition-colors hover:text-foreground"
							>
								Conversations
							</Link>
						</li>
						<span className="text-muted-foreground">/</span>
						<li className="text-foreground font-medium">
							{conversation.title || "Untitled"}
						</li>
					</ol>
				</nav>

				<div className="mb-7 flex items-start justify-between gap-6">
					<div>
						<h1 className="console-title mb-3 text-3xl leading-tight text-foreground md:text-4xl">
							{conversation.title || "Untitled"}
						</h1>
						<div className="flex items-center gap-4 text-sm text-muted-foreground flex-wrap">
							<div className="flex items-center gap-1.5">
								<code className="console-code px-2 py-1 text-xs font-mono">
									{conversationId}
								</code>
								<CopyButton value={conversationId} iconSize={3.5} />
							</div>
							<span>Owner: {conversation.ownerUserId}</span>
							{conversation.clientId && (
								<span>Client: <code className="font-mono">{conversation.clientId}</code></span>
							)}
							{conversation.agentId && (
								<span>Agent: <code className="font-mono">{conversation.agentId}</code></span>
							)}
						</div>
						{conversation.startedByConversationId && (
							<div className="flex items-center gap-2 text-sm text-muted-foreground mt-1">
								<span>Child of</span>
								<Link
									to="/conversations/$conversationId"
									params={{ conversationId: conversation.startedByConversationId }}
									search={conversation.startedByEntryId ? { entryId: conversation.startedByEntryId } : undefined}
									className="text-primary hover:underline font-mono text-xs"
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
											className="text-primary hover:underline font-mono text-xs"
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
							className={cn(
								"console-segment",
								activeTab === "entries" && "console-segment-active",
							)}
						>
							Entries
						</button>
						<button
							onClick={() => setActiveTab("memberships")}
							className={cn(
								"console-segment",
								activeTab === "memberships" && "console-segment-active",
							)}
						>
							Memberships
						</button>
					</nav>
				</div>

				{/* Tab Content */}
				{activeTab === "entries" ? (
					<div>
						{/* Channel Filter */}
						<div className="mb-5 flex items-center gap-2">
							<div className="console-segmented">
								{(
									["all", "history", "context"] as ChannelFilter[]
								).map((channel) => (
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

						{/* Entries List */}
						<div className="space-y-4">
							{filteredEntries.map((entry) => (
								<EntryCard
									key={entry.id}
									entry={entry}
									formatDate={formatDate}
									isHighlighted={highlightedEntryId === entry.id}
									ref={getEntryRef(entry.id)}
									onClick={() => selectEntry(entry.id)}
									channelFilter={channelFilter}
								/>
							))}
						</div>
					</div>
				) : (
					<MembershipsTab memberships={memberships} />
				)}
			</div>

			<aside className="hidden w-80 flex-shrink-0 overflow-auto border-l border-[rgba(43,39,34,0.1)] pl-7 lg:block">
				<h3 className="text-sm font-semibold text-foreground mb-4 flex items-center gap-2">
					<GitFork className="w-4 h-4 text-primary forks-icon" />
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
				<p className="text-xs text-muted-foreground mt-4 pt-4 border-t border-[rgba(43,39,34,0.1)]">
					{forkTree.length} conversation{forkTree.length !== 1 ? "s" : ""} in
					this fork tree
				</p>

				{/* Child Conversations (agent lineage) */}
				{childConversations.length > 0 && (
					<>
						<h3 className="text-sm font-semibold text-foreground mt-6 mb-4 flex items-center gap-2">
							<Network className="w-4 h-4 text-primary" />
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
									<div className="text-sm font-medium text-foreground truncate">
										{child.title || "Untitled"}
									</div>
									<code
										className="font-mono text-xs text-muted-foreground block truncate mt-1"
										title={child.id}
									>
										{child.id}
									</code>
									{child.startedByEntryId && (
										<div className="text-xs text-muted-foreground mt-1">
											Started at entry{" "}
											<span className="font-mono">{child.startedByEntryId.slice(0, 8)}...</span>
										</div>
									)}
									<div className="text-xs text-muted-foreground mt-1">
										{formatShortDate(child.createdAt || "")}
									</div>
								</Link>
							))}
						</div>
						<p className="text-xs text-muted-foreground mt-4 pt-4 border-t border-[rgba(43,39,34,0.1)]">
							{childConversations.length} child conversation{childConversations.length !== 1 ? "s" : ""}
						</p>
					</>
				)}
			</aside>
		</div>
	);
}

// Entry Card Component
const EntryCard = React.forwardRef<
	HTMLDivElement,
	{
		entry: EntryWithForkPoint;
		formatDate: (date: string) => string;
		isHighlighted?: boolean;
		onClick?: () => void;
		channelFilter?: string;
	}
>(({ entry, formatDate, isHighlighted, onClick, channelFilter }, ref) => {
	const { viewMode, setViewMode, hasCustomRenderer } = useContentViewMode(
		entry.contentType || "",
	);

	const getChannelColor = (channel: string) => {
		switch (channel) {
			case "history":
				return "bg-sage-soft text-primary";
			case "context":
				return "bg-[#eadccd] text-[#98613d]";
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
				entry.isForkPoint
					? "ring-1 ring-primary/20"
					: "",
				isHighlighted && "ring-2 ring-primary ring-offset-2 border-primary",
			)}
		>
			{/* Header */}
			<div className="flex items-center justify-between mb-2">
				<div className="flex items-center gap-2">
					<Badge
						className={cn(
							"text-xs font-medium",
							getChannelColor(entry.channel || ""),
						)}
					>
						{entry.channel}
					</Badge>
					{entry.epoch !== undefined && (
						<Badge className="text-xs font-medium bg-[#eadccd] text-[#98613d]">
							epoch: {entry.epoch}
						</Badge>
					)}
					<div className="flex items-center gap-1 min-w-0 flex-1">
						<span className="text-xs text-muted-foreground font-mono truncate" title={entry.id}>
							{entry.id}
						</span>
						<CopyButton value={entry.id || ""} iconSize={3} className="shrink-0" />
					</div>
					{entry.isForkPoint && (
						<ForkPointBadge
							forksAtPoint={entry.forksAtPoint}
							channelFilter={channelFilter}
						/>
					)}
				</div>
				<div onClick={(e) => e.stopPropagation()}>
					<TimestampPopover
						timestamp={entry.createdAt || ""}
						displayText={formatDate(entry.createdAt || "")}
					/>
				</div>
			</div>

			{/* User, content type, and view toggle */}
			<div className="flex items-center justify-between text-sm text-muted-foreground mb-2">
				<div className="flex items-center gap-4">
					{entry.userId && <span>User: {entry.userId}</span>}
				</div>
				<div className="flex items-center gap-3">
					<span>Type: {entry.contentType}</span>
					{hasCustomRenderer && (
						<ViewToggle mode={viewMode} onChange={setViewMode} />
					)}
				</div>
			</div>

			{/* Content */}
			<ContentRenderer
				content={entry.content as unknown[]}
				contentType={entry.contentType || ""}
				viewMode={viewMode}
			/>
		</div>
	);
});
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
	// Build search params preserving channel filter
	const searchParams: { forkedAt?: string; channel?: ChannelParam } = {};
	if (fork.forkedAtEntryId) {
		searchParams.forkedAt = fork.forkedAtEntryId;
	}
	if (channelFilter !== "all") {
		searchParams.channel = channelFilter;
	}

	return (
		<Link
			to="/conversations/$conversationId"
			params={{ conversationId: fork.id }}
			search={Object.keys(searchParams).length > 0 ? searchParams : undefined}
			className={cn(
				"console-panel block rounded-xl p-3 transition-colors",
				isActive
					? "bg-sage-soft/45 ring-1 ring-primary/20 fork-card-active"
					: "hover:bg-sage-soft/25",
			)}
		>
			<code
				className="font-mono text-xs text-foreground block truncate"
				title={fork.id}
			>
				{fork.id}
			</code>
			{!fork.isRoot && fork.forkedAtEntryId && (
				<div className="text-xs text-muted-foreground mt-1">
					Forked at{" "}
					<button
						onClick={(e) => e.preventDefault()}
						className="text-primary hover:underline font-mono"
					>
						{fork.forkedAtEntryId.slice(0, 8)}...
					</button>
				</div>
			)}
			<div className="text-xs text-muted-foreground mt-1">
				Updated {formatDate(fork.updatedAt)}
			</div>
		</Link>
	);
}

// Memberships Tab Component
function MembershipsTab({
	memberships,
}: {
	memberships: ConversationMembership[];
}) {
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
						<th>
							User ID
						</th>
						<th>
							Access Level
						</th>
						<th>
							Added
						</th>
					</tr>
				</thead>
				<tbody>
					{memberships.map((membership, idx) => (
						<tr key={idx}>
							<td>
								<code className="text-sm font-mono">{membership.userId}</code>
							</td>
							<td>
								<Badge
									className={cn(
										"capitalize",
										getAccessLevelBadge(membership.accessLevel || ""),
									)}
								>
									{membership.accessLevel}
								</Badge>
							</td>
							<td className="text-sm text-muted-foreground">
								{formatDate(membership.createdAt || "")}
							</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

// Made with Bob
