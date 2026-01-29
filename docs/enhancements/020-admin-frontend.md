# Enhancement 020: Admin Frontend

## Overview

This document outlines the design and implementation plan for a modern, reactive admin frontend for the Memory Service. The frontend provides administrative access to conversations, entries, and system management functions for users with admin or auditor roles.

## Goals

1. Provide a clean, modern administrative interface for Memory Service management
2. Support both admin (full access) and auditor (read-only) roles
3. Enable efficient browsing, searching, and management of conversations across all users
4. Support soft-delete/restore workflows and data retention management
5. Provide audit logging through a persistent session justification field

## Target Users

| Role | Capabilities |
|------|-------------|
| **Admin** | Full access: view, delete, restore conversations; run eviction jobs |
| **Auditor** | Read-only access: view conversations, entries, memberships; search |

## Session Justification

All admin API operations accept a `justification` parameter for audit logging. Rather than prompting for justification on each action, the frontend provides a **persistent session justification field** that:

- Appears prominently in the header/toolbar area on all screens
- Is always visible and easily editable
- Automatically applies to all API calls made during the session
- Shows a visual indicator when empty (warning state) to encourage compliance
- Persists across page navigation within the session

**UI Placement**: A collapsible input field in the top header bar, showing a truncated preview when collapsed. Clicking expands to show the full text and allows editing. An amber warning icon appears when the field is empty.

**Example justifications**:
- "Investigating support ticket #1234"
- "Quarterly compliance audit - Q1 2025"
- "User account cleanup per deletion request"

This approach reduces friction for admins performing multiple related operations while ensuring all actions are properly logged for compliance.

## Core User Activities

Based on the Admin API (`openapi-admin.yml`), the frontend supports five primary activities:

### 1. Dashboard & System Health
- View service health status
- Quick access to recent activity
- Summary statistics (total conversations, deleted items pending eviction, active jobs)

### 2. Conversation Browser
- List conversation groups (fork trees) across all users:
  - Shows only the most recently updated conversation per fork tree
  - Conversations sharing a common root are grouped together
- Filtering:
  - By user ID (owner)
  - By deleted status (active, deleted, or all)
  - By updated at date
- Pagination for large result sets
- Quick actions: view details, delete, restore

### 3. Conversation Detail View
- Full conversation metadata (title, owner, timestamps)
- **Forks Panel**: Lists all conversations in the fork group
  - Simple list sorted by last updated (descending)
  - Shows each fork with its title, ID, fork point entry, and last updated timestamp
  - Currently selected fork is highlighted
  - Clicking a fork switches the entries view to that conversation
  - Clicking a fork-at entry ID scrolls to and highlights the entry where the fork occurred
- Entries tab: Browse all entries with channel filtering (history, memory, summary)
- Memberships tab: View all users with access and their access levels
- Deletion status and restore capability for soft-deleted conversations

### 4. System-Wide Search
- Semantic search across all conversations
- Filter by user ID to scope search
- Include/exclude deleted content
- Results show matching entries with relevance scores and highlights

### 5. Data Eviction Management
- Configure and run eviction jobs:
  - Retention period (ISO 8601 duration)
  - Resource types to evict (conversation_groups, conversation_memberships)
- Monitor currently running jobs with progress tracking

## Screen Inventory

### Primary Screens

| Screen | File | Description |
|--------|------|-------------|
| Dashboard | `dashboard.html` | Overview with health status, stats, quick actions |
| Conversation List | `conversations.html` | Filterable table of all conversations |
| Conversation Detail | `conversation-detail.html` | Single conversation with entries and memberships |
| Search | `search.html` | System-wide semantic search interface |
| Eviction | `eviction.html` | Data cleanup job management |

### Secondary Components

| Component | Description |
|-----------|-------------|
| Header Bar | Service name, session justification field, user info |
| Navigation | Sidebar with role-based menu items |
| Filters Panel | Reusable filter controls for lists |
| Data Tables | Sortable, paginated tables |
| Modals | Confirmation dialogs for destructive actions |
| Toast Notifications | Success/error feedback |

## Design Principles

### Visual Design
- **Clean and professional**: Minimal, business-focused aesthetic
- **Information density**: Show relevant data without clutter
- **Clear hierarchy**: Important actions and data are prominent
- **Status indicators**: Color-coded badges for states (active, deleted, roles)

### Color Palette
- **Primary**: Indigo/blue tones for actions and navigation
- **Success**: Green for active/healthy states
- **Warning**: Amber for pending/attention states
- **Danger**: Red for deleted/destructive actions
- **Neutral**: Gray scale for text and backgrounds

### Layout
- Fixed header bar with session justification and user info
- Fixed sidebar navigation (collapsible on mobile)
- Main content area with consistent padding
- Responsive design (desktop-first, mobile-friendly)

## Phase 0.5: Style Exploration

Before building out all screens, we explore different visual styles using the conversation-detail page as a representative sample. This allows stakeholders to evaluate and choose a design direction before committing to full implementation.

### Objectives
- Generate multiple distinct visual styles for comparison
- Explore different color palettes, layouts, and design patterns
- Get stakeholder buy-in on visual direction before Phase 1

### Style Mockups

All style mockups are placed in `common/admin-webui/style-mockups/` and implement the same conversation-detail screen with different aesthetics:

| Style | File | Description |
|-------|------|-------------|
| **Minimal Light** | `minimal-light.html` | Clean whitespace-focused design with subtle shadows, light grays, and restrained color use. Emphasizes content over chrome. |
| **Dark Mode Pro** | `dark-mode-pro.html` | Dark background with high contrast text, cyan/purple accent colors. Professional feel suited for extended use. |
| **Terminal** | `terminal.html` | Terminal-inspired dark theme with charcoal background, mint green (#48d597) accent, two-column property grids, uppercase tracking labels, and clean data tables. |

### Evaluation Criteria

When reviewing style mockups, consider:
- **Readability**: Is text easy to read? Is there sufficient contrast?
- **Information density**: Does it show enough data without feeling cluttered?
- **Professionalism**: Does it feel appropriate for an admin tool?
- **Accessibility**: Are colors and contrast suitable for all users?
- **Consistency potential**: Can this style scale across all screens?

### File Structure (Style Exploration)
```
common/admin-webui/
├── style-mockups/                          # Phase 0.5: Style exploration
│   ├── minimal-light.html                  # Clean, whitespace-focused
│   ├── dark-mode-pro.html                  # Dark theme, high contrast
│   └── terminal.html                       # Terminal-inspired dark theme
```

## Phase 1: Static HTML Mockups

### Objectives
- Create self-contained HTML pages styled with Tailwind CSS 4.0
- Apply the chosen style from Phase 0.5 consistently across all screens
- Iterate on visual design and layout before implementing functionality
- Establish design patterns for tables, forms, modals, and navigation

### Technology Stack
- **Tailwind CSS 4.0** via CDN
- **Heroicons** for iconography (SVG inline)
- Self-contained HTML files (no build step required)

### CDN Resources
```html
<!-- Tailwind CSS 4.0 -->
<script src="https://cdn.tailwindcss.com"></script>

<!-- Optional: Inter font for cleaner typography -->
<link rel="stylesheet" href="https://rsms.me/inter/inter.css">
```

### File Structure
```
common/admin-webui/
├── style-mockups/                          # Phase 0.5: Style exploration
│   └── ...
├── mockups/                                # Phase 1: Static HTML mockups
│   ├── dashboard.html                      # Main dashboard
│   ├── conversations.html                  # Conversation list with filters
│   ├── conversation-detail.html            # Single conversation view (Entries tab)
│   ├── conversation-detail-memberships.html # Single conversation view (Memberships tab)
│   ├── search.html                         # Semantic search interface
│   └── eviction.html                       # Eviction job management
├── src/                                    # Future: JavaScript/React source
└── README.md                               # Project documentation
```

### Mockup Specifications

#### dashboard.html
- Header bar with:
  - Service name
  - Session justification field (collapsible, warning state when empty)
  - User info with role badge
- Health status card (green checkmark when healthy)
- Statistics cards:
  - Total conversations
  - Active conversations
  - Soft-deleted (pending eviction)
  - Users with conversations
- Recent activity list (last 5 conversations accessed/modified)
- Quick action buttons (View Conversations, Search, Run Eviction)

#### conversations.html
- Filter panel (collapsible):
  - User ID text input with search
  - Deleted status: radio (All / Active Only / Deleted Only)
  - Date range pickers for deletion date
  - Apply/Clear buttons
- Results table:
  - Columns: ID, Title, Owner, Created, Updated, Deleted At, Status, Actions
  - Status badges: Active (green), Deleted (red)
  - Actions: View, Delete (admin), Restore (admin, for deleted)
- Pagination controls
- Confirmation modal for destructive actions (uses session justification)

#### conversation-detail.html
- Breadcrumb navigation
- Conversation header:
  - Title (or "Untitled")
  - ID (UUID) with copy button
  - Owner user ID
  - Created/Updated timestamps
  - Deleted banner if soft-deleted (with Restore button)
- Forks panel (sidebar):
  - Header: "Forks"
  - Simple list sorted by last updated (descending)
  - Each item shows: title, ID, forked-at entry ID, last updated timestamp
  - Root conversation shows "Root conversation" instead of forked-at
  - Currently selected conversation is highlighted
  - Clicking switches the entry view to that conversation
  - Footer: "X forks in the conversation"
- Tab navigation: Entries | Memberships
- Entries tab:
  - Channel filter: All / History / Memory / Summary
  - Entry cards showing:
    - Entry ID (UUID), User ID
    - Channel badge
    - Content type
    - Content preview (truncated JSON)
    - Timestamp
    - Fork indicator if other conversations forked at this entry
  - Clicking a fork-at entry ID in the panel scrolls to and highlights the fork point entry
  - Pagination
- Memberships tab:
  - Table: User ID, Access Level, Created At
  - Access level badges: Owner (purple), Manager (blue), Writer (green), Reader (gray)

#### search.html
- Search form:
  - Query text input (large, prominent)
  - Advanced options (collapsible):
    - User ID filter
    - Conversation IDs (comma-separated)
    - Include deleted toggle
    - Top K results slider (5-100)
  - Search button (uses session justification for audit)
- Results section:
  - Result count and search metadata
  - Result cards:
    - Score/relevance indicator
    - Entry content preview
    - Highlighted matches
    - Conversation link
    - Entry metadata (channel, timestamp)
- Empty state for no results

#### eviction.html
- Running Jobs section:
  - Shows only currently running eviction jobs
  - Columns: Job ID (UUID), Progress, Started
  - Progress bar for each running job
  - Empty state when no jobs are running
- New Eviction Job form:
  - Retention period input with format hint (e.g., "P90D")
  - Resource types checkboxes:
    - [ ] Conversation Groups
    - [ ] Conversation Memberships
  - Execution mode: Synchronous / Async
  - Start Eviction button (with confirmation modal, uses session justification)
- Warning banner about irreversibility

## Data Models Reference

Key schemas from the Admin API for mockup data. Note: All IDs (conversation, entry, job) are UUIDs.

### AdminConversationSummary
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "title": "Project Discussion",
  "ownerUserId": "user_456",
  "createdAt": "2025-01-15T10:30:00Z",
  "updatedAt": "2025-01-20T14:22:00Z",
  "deletedAt": null,
  "lastMessagePreview": "Let me summarize the key points...",
  "accessLevel": "owner"
}
```

### Entry
```json
{
  "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "conversationId": "550e8400-e29b-41d4-a716-446655440000",
  "userId": "user_456",
  "channel": "history",
  "epoch": null,
  "contentType": "message",
  "content": [{"type": "text", "text": "Hello, how can I help?"}],
  "createdAt": "2025-01-15T10:31:00Z"
}
```

### ConversationMembership
```json
{
  "conversationId": "550e8400-e29b-41d4-a716-446655440000",
  "userId": "user_789",
  "accessLevel": "reader",
  "createdAt": "2025-01-16T09:00:00Z"
}
```

### ConversationForkSummary
```json
{
  "conversationId": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "forkedAtEntryId": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "forkedAtConversationId": "550e8400-e29b-41d4-a716-446655440000",
  "title": "Alternative approach discussion",
  "createdAt": "2025-01-16T14:00:00Z"
}
```

### EvictionJobResponse
```json
{
  "jobId": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "status": "RUNNING",
  "progress": 45,
  "createdAt": "2025-01-28T08:00:00Z",
  "completedAt": null,
  "error": null
}
```

## Future Phases (Out of Scope)

### Phase 2: Interactive JavaScript
- API integration with fetch/axios
- Dynamic filtering and pagination
- Real-time job progress updates (SSE)
- Form validation and submission

### Phase 3: React/Vue Migration
- Component-based architecture
- State management
- Routing
- Build tooling integration

### Phase 4: Authentication Integration
- OIDC/OAuth2 login flow
- Role-based UI rendering
- Session management

## Success Criteria for Phase 1

1. All five primary screens are implemented as static HTML
2. Consistent visual design across all screens
3. Responsive layout works on desktop and tablet
4. All interactive states represented (hover, active, disabled)
5. Empty states and loading states mocked
6. Design is reviewed and approved before Phase 2

## Open Questions

1. **Audit Log Viewer**: Should we add a dedicated screen for viewing audit logs, or is this handled by external logging infrastructure?

2. **Bulk Operations**: Should the conversation list support bulk selection for batch delete/restore?

3. **Export**: Should search results or conversation data be exportable (CSV/JSON)?

4. **Dark Mode**: Should we support dark mode from the start, or add it later?

## References

- Admin API Spec: `memory-service-contracts/src/main/resources/openapi-admin.yml`
- User API Spec: `memory-service-contracts/src/main/resources/openapi.yml`
- Tailwind CSS 4.0: https://tailwindcss.com/docs
