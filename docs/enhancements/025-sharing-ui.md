---
status: implemented
---

# Conversation Sharing UI

> **Status**: Implemented.

## Overview

This document outlines the design for adding conversation sharing capabilities to the chat application. Users will be able to share conversations with other users, view current members, manage access levels, and transfer ownership.

## Goals

1. Allow users to share conversations with other users at different access levels
2. Display current conversation members and their permissions
3. Provide intuitive access level management
4. Enable ownership transfer with appropriate safeguards
5. Maintain visual consistency with the Minimal Light design system

## Limitations

This initial implementation has the following constraints:

- **User IDs only**: Members are identified by user ID strings. Display names and email addresses are not currently available from the API.
- **No user search/autocomplete**: Users must know the exact user ID to share with.
- **No invitation flow**: Users must already exist in the system to be added as members.

## API Endpoints

The following endpoints from the Memory Service API will be used:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/conversations/{conversationId}/memberships` | GET | List all members with access |
| `/v1/conversations/{conversationId}/memberships` | POST | Share with a new user |
| `/v1/conversations/{conversationId}/memberships/{userId}` | PATCH | Update access level |
| `/v1/conversations/{conversationId}/memberships/{userId}` | DELETE | Remove member |
| `/v1/ownership-transfers` | GET | List pending transfers |
| `/v1/ownership-transfers` | POST | Create ownership transfer |
| `/v1/ownership-transfers/{transferId}` | GET | Get transfer details |
| `/v1/ownership-transfers/{transferId}/accept` | POST | Accept transfer |
| `/v1/ownership-transfers/{transferId}` | DELETE | Cancel/decline transfer |

### Access Levels

| Level | Permissions |
|-------|-------------|
| `owner` | Full control, can delete, transfer ownership, manage all members |
| `manager` | Can share with others, manage writer/reader access |
| `writer` | Can send messages and create forks |
| `reader` | View-only access |

### Permission Rules

| Action | Owner | Manager | Writer | Reader |
|--------|:-----:|:-------:|:------:|:------:|
| View members | ✅ | ✅ | ✅ | ✅ |
| Add members | ✅ | ✅ | ❌ | ❌ |
| Change access levels | ✅ | ✅* | ❌ | ❌ |
| Remove members | ✅ | ✅* | ❌ | ❌ |
| Transfer ownership | ✅ | ❌ | ❌ | ❌ |

*Managers can only manage writer/reader access, not other managers or the owner.

## Design Inspiration

Drawing from established sharing patterns in popular applications:

### Google Docs/Drive
- "Share" button prominently placed in header
- Modal with email input and permission dropdown
- List of current collaborators with roles
- "Transfer ownership" as advanced option

### Notion
- Clean sharing popover
- Role indicators with icons
- Guest vs member distinction

### Figma
- Share button triggers modal
- Permission dropdown per user
- Copy link for easy sharing
- Owner crown indicator

### Slack
- Channel membership management
- Clear role hierarchy
- Invite via email or username

## UI Design

### 1. Share Button Placement

Add a "Share" button to the chat panel header. All members can click this button to view who has access to the conversation. Only owners and managers will see the controls to add/remove members.

```
┌─────────────────────────────────────────────────────────────────┐
│ Chat with your agent                              [👥 Share]    │
│ Conversation about React hooks                                   │
├─────────────────────────────────────────────────────────────────┤
```

**Button Styling** (Minimal Light design):
- Icon: Users icon (`👥` or Lucide `Users`)
- Background: `bg-mist` on hover
- Border: `border border-stone/20 rounded-lg`
- Text: `text-sm text-stone hover:text-ink`

**Tooltip**:
- Owner/Manager: "Share conversation"
- Writer/Reader: "View members"

### 2. Share Modal

A modal dialog for viewing and managing conversation sharing. The UI adapts based on the current user's access level.

#### Owner/Manager View (Can Manage)

Owners and managers see the full sharing controls:

```
┌─────────────────────────────────────────────────────────────────┐
│                                                              ✕  │
│  Share "Help with React hooks"                                  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👤 Enter user ID                                         │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
│  People with access                                             │
│  ─────────────────────────────────────────────────────────────  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👤 user_john_doe                            Owner    👑   │  │
│  │    Added Jan 15, 2025                                     │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👤 user_jane_smith                     [Manager ▾]   🗑   │  │
│  │    Added Jan 20, 2025                                     │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👤 user_bob_wilson                      [Reader ▾]   ⋮   │  │
│  │    Added Jan 22, 2025                                     │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Notes**:
- Managers see dropdowns and overflow menus (⋮) for writers/readers only. They cannot modify other managers or the owner.
- The overflow menu contains "Remove" and (for owners only) "Transfer ownership".
- Owners see the overflow menu on all members.

#### Writer/Reader View (Read-Only)

Writers and readers see a simplified view without editing controls:

```
┌─────────────────────────────────────────────────────────────────┐
│                                                              ✕  │
│  "Help with React hooks"                                        │
│                                                                 │
│  People with access                                             │
│  ─────────────────────────────────────────────────────────────  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👤 user_john_doe                            Owner    👑   │  │
│  │    Added Jan 15, 2025                                     │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👤 user_jane_smith                          Manager  🔧   │  │
│  │    Added Jan 20, 2025                                     │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👤 user_bob_wilson                          Reader   👁   │  │
│  │    Added Jan 22, 2025                           (you)     │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Differences from manager view**:
- No "Add people" input field
- No access level dropdowns (static text instead)
- No remove buttons
- No "Advanced" section
- "(you)" indicator shows which entry is the current user

### 3. Add Member Section

**Only visible to owners and managers.** When adding a new member:

```
┌───────────────────────────────────────────────────────────────┐
│ 👤 user_alice                                 [Writer ▾]      │
└───────────────────────────────────────────────────────────────┘
                                                    [  Add  ]
```

**Interaction Flow**:
1. User types user ID in input field
2. Selects access level from dropdown (defaults to "Reader")
3. Clicks "Add" button
4. New member appears in list with success feedback

**Access Level Dropdown Options**:
- Manager - Can share with others
- Writer - Can send messages
- Reader - View only

### 4. Member List Item States

**Owner Row**:
- Crown icon (`👑`) indicates ownership
- No dropdown or remove button (owner cannot be removed)
- "Owner" text is static, not a dropdown
- Transfer ownership available only to current owner (in Advanced section)

**Manager Row** (when current user is owner):
- Wrench icon (`🔧`)
- Access level dropdown (can demote to writer/reader)
- Remove button available

**Manager Row** (when current user is also a manager):
- Wrench icon visible
- No dropdown or remove button (managers cannot modify other managers)

**Writer/Reader Row** (when current user is owner or manager):
- Pencil (`✏️`) or Eye (`👁`) icon
- Access level dropdown
- Remove button (trash icon)
- Hover state: `bg-mist rounded-lg`

**Any Row** (when current user is writer or reader):
- Icon for access level
- Static text for access level (no dropdown)
- No remove button
- "(you)" indicator if this is the current user

**Pending/Processing State**:
- Subtle spinner replacing action buttons
- Disabled dropdown during update

### 5. Transfer Ownership Flow

**Only available to the conversation owner.** Ownership transfer requires acceptance by the recipient. See [Enhancement 024](./024-sharing-api-enhancements.md) for API details.

#### Initiating a Transfer (Owner)

Since transfers can only be made to existing members, the "Transfer ownership" action appears as a menu option on each member row (visible only to the owner). This is more intuitive than a separate section with a member dropdown.

**Member Row with Transfer Option** (Owner view):

```
┌───────────────────────────────────────────────────────────────┐
│ 👤 user_jane_smith                     [Manager ▾]   ⋮       │
│    Added Jan 20, 2025                              ┌───────┐ │
│                                                    │ Remove│ │
│                                                    │ ───── │ │
│                                                    │ 👑 Transfer │
│                                                    │ ownership   │
│                                                    └───────┘ │
└───────────────────────────────────────────────────────────────┘
```

When the owner clicks "Transfer ownership" on a member, a confirmation modal opens:

```
┌─────────────────────────────────────────────────────────────────┐
│                                                              ✕  │
│  Transfer ownership                                             │
│                                                                 │
│  Transfer ownership to user_jane_smith?                         │
│                                                                 │
│  They will need to accept the transfer.                         │
│  You will become a Manager after they accept.                   │
│                                                                 │
│                            [ Cancel ]  [ Request Transfer ]     │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Interaction Flow (Owner)**:
1. Owner clicks the overflow menu (⋮) on a member row
2. Owner selects "Transfer ownership" from the menu
3. Confirmation modal opens showing the selected member
4. Owner confirms with "Request Transfer" button
5. On success: Transfer created as "pending", UI shows pending state on that member row

#### Pending Transfer State (Owner View)

When a pending transfer exists, the member row shows the pending state:

```
┌───────────────────────────────────────────────────────────────┐
│ 👤 user_jane_smith                          Manager  🔧       │
│    Added Jan 20, 2025                                         │
│    ┌─────────────────────────────────────────────────────┐    │
│    │ ⏳ Transfer pending · Waiting for acceptance        │    │
│    │                              [ Cancel Transfer ]    │    │
│    └─────────────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────────────┘
```

**Notes:**
- The pending transfer banner is shown inline on the recipient's member row
- Access level dropdown and remove button are hidden while transfer is pending
- Only one pending transfer can exist per conversation
- Owner can cancel the transfer from this inline banner

#### Incoming Transfer (Recipient View)

When the recipient opens the share modal, they see a banner:

```
┌─────────────────────────────────────────────────────────────────┐
│                                                              ✕  │
│  "Help with React hooks"                                        │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👑 Ownership transfer request                             │  │
│  │    user_john_doe wants to transfer ownership to you       │  │
│  │                                                           │  │
│  │                    [ Decline ]  [ Accept Ownership ]      │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
│  People with access                                             │
│  ...                                                            │
└─────────────────────────────────────────────────────────────────┘
```

**Interaction Flow (Recipient)**:
1. Recipient opens share modal and sees transfer request banner
2. Recipient clicks "Accept Ownership" or "Decline"
3. On accept: Recipient becomes owner, previous owner becomes manager
4. On decline: Transfer is deleted (hard delete), ownership unchanged

#### Constraints

- Only one pending transfer can exist per conversation
- Owner cannot initiate a new transfer while one is pending
- Either party can delete a pending transfer (owner cancels, recipient declines)
- Both cancel and decline use `DELETE /v1/transfers/{transferId}`

### 6. Access Level Indicators

Visual indicators for different access levels throughout the UI:

| Level | Icon | Color | Description |
|-------|------|-------|-------------|
| Owner | 👑 Crown | `text-terracotta` | Full control |
| Manager | 🔧 Wrench | `text-sage` | Can manage sharing |
| Writer | ✏️ Pencil | `text-ink` | Can contribute |
| Reader | 👁 Eye | `text-stone` | View only |

### 7. Empty State (No Additional Members)

When conversation has no shared members:

```
┌─────────────────────────────────────────────────────────────────┐
│                                                                 │
│  People with access                                             │
│  ─────────────────────────────────────────────────────────────  │
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ 👤 user_john_doe                            Owner    👑   │  │
│  └───────────────────────────────────────────────────────────┘  │
│                                                                 │
│           ┌─────────────────────────────────────────┐           │
│           │   This conversation is private.         │           │
│           │   Add people above to collaborate.      │           │
│           └─────────────────────────────────────────┘           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Component Architecture

### New Components

| Component | File | Description |
|-----------|------|-------------|
| `ShareButton` | `share-button.tsx` | Header button triggering share modal |
| `ShareModal` | `share-modal.tsx` | Main sharing dialog |
| `MemberList` | `member-list.tsx` | List of conversation members |
| `MemberListItem` | `member-list-item.tsx` | Individual member row |
| `AddMemberForm` | `add-member-form.tsx` | Input for adding new members |
| `AccessLevelSelect` | `access-level-select.tsx` | Dropdown for access levels |
| `MemberOverflowMenu` | `member-overflow-menu.tsx` | Overflow menu with Remove/Transfer actions |
| `TransferConfirmModal` | `transfer-confirm-modal.tsx` | Confirm ownership transfer (owner) |
| `TransferPendingBanner` | `transfer-pending-banner.tsx` | Inline pending transfer status on member row |
| `IncomingTransferBanner` | `incoming-transfer-banner.tsx` | Accept/decline transfer (recipient) |

### Component Hierarchy

```
ChatPanel
├── ChatHeader
│   └── ShareButton → triggers ShareModal
│       └── ShareModal
│           ├── IncomingTransferBanner (if recipient of pending transfer)
│           ├── AddMemberForm (owner/manager only)
│           │   └── AccessLevelSelect
│           └── MemberList
│               └── MemberListItem
│                   ├── AccessLevelSelect
│                   ├── MemberOverflowMenu (owner/manager only)
│                   │   └── TransferConfirmModal (owner only, on "Transfer" click)
│                   └── TransferPendingBanner (if this member has pending transfer)
```

### State Management

```typescript
// React Query hooks for data fetching
useMemberships(conversationId)      // GET memberships
useShareConversation()              // POST new member
useUpdateMembership()               // PATCH access level
useRemoveMembership()               // DELETE member

// Transfer-related hooks (see Enhancement 024)
usePendingTransfer(conversationId)  // GET pending transfer for conversation
useRequestTransfer()                // POST create transfer
useAcceptTransfer()                 // POST accept transfer
useDeleteTransfer()                 // DELETE transfer (cancel or decline)

// Derived state from memberships
currentUserAccessLevel: AccessLevel // Determines UI capabilities
canManageMembers: boolean           // owner or manager
canTransferOwnership: boolean       // owner only

// Derived state from pending transfer
hasPendingTransfer: boolean         // Is there a pending transfer?
isTransferRecipient: boolean        // Is current user the recipient?
isTransferSender: boolean           // Is current user the sender?

// Local state
isShareModalOpen: boolean
transferTargetUserId: string | null // User ID when transfer confirm modal is open
pendingMemberId: string | null      // For optimistic updates
```

## Design Tokens

Following the Minimal Light design system from Enhancement 022/023:

### Colors
```css
/* Backgrounds */
--modal-bg: #FDFBF7;          /* cream */
--modal-overlay: rgba(26, 26, 26, 0.4);
--member-hover: #f5f3ef;      /* mist */

/* Text */
--text-primary: #1a1a1a;      /* ink */
--text-secondary: #8a8580;    /* stone */

/* Accents */
--owner-color: #c67a5c;       /* terracotta */
--manager-color: #7d9a8c;     /* sage */
--danger-color: #c67a5c;      /* terracotta - for remove */
```

### Typography
```css
/* Modal title */
font-family: 'Instrument Serif', Georgia, serif;
font-size: 1.25rem; /* text-xl */

/* Member user IDs */
font-family: 'DM Sans', system-ui, sans-serif;
font-size: 0.9375rem; /* text-base */
font-weight: 500;

/* Secondary text */
font-size: 0.75rem; /* text-xs */
color: var(--text-secondary);
```

### Spacing
```css
/* Modal */
--modal-padding: 1.5rem;      /* p-6 */
--modal-max-width: 28rem;     /* max-w-md */
--modal-radius: 1rem;         /* rounded-2xl */

/* Member items */
--member-padding: 0.75rem;    /* p-3 */
--member-gap: 0.5rem;         /* gap-2 */
```

## Interaction Patterns

### Keyboard Navigation
- `Escape` closes modal
- `Tab` navigates between focusable elements
- `Enter` submits forms
- Arrow keys navigate dropdown options

### Loading States
- Skeleton placeholders while loading memberships
- Spinner on action buttons during mutations
- Disabled state for buttons during processing

### Error Handling
- Toast notifications for errors
- Inline validation for user ID input (non-empty)
- Server-side validation for user existence
- Retry option for failed requests

### Success Feedback
- Brief success toast on member added/removed
- Smooth list animation when members change
- Modal stays open for multiple additions

## Mobile Considerations

For smaller screens:
- Modal becomes full-screen sheet sliding from bottom
- Member list scrollable with sticky header
- Touch-friendly tap targets (44px minimum)
- Swipe to dismiss modal

## Accessibility

- ARIA labels on all interactive elements
- Focus trap within modal
- Screen reader announcements for state changes
- High contrast mode support
- Reduced motion preference respected

## Implementation Phases

### Phase 1: Read-Only Sharing View
1. Add ShareButton to header (visible to all members)
2. Implement ShareModal with MemberList
3. Fetch and display current memberships
4. Show owner badge and access levels
5. Determine current user's access level from memberships

### Phase 2: Permission-Aware UI
1. Show/hide AddMemberForm based on access level (owner/manager only)
2. Show/hide action controls based on permissions
3. Implement read-only view for writers/readers
4. Add "(you)" indicator for current user

### Phase 3: Add/Remove Members
1. Implement AddMemberForm (owner/manager)
2. Add member with selected access level
3. Remove member functionality
4. Managers restricted to managing writers/readers only
5. Error handling and validation

### Phase 4: Access Level Management
1. AccessLevelSelect dropdown
2. Update membership access level
3. Managers can only change writer/reader levels
4. Optimistic updates with rollback

### Phase 5: Ownership Transfer
1. Fetch pending transfer when opening share modal
2. Add overflow menu to member rows with "Remove" and "Transfer ownership" options
3. TransferConfirmModal when owner clicks "Transfer ownership" on a member
4. Inline pending transfer banner on recipient's member row (with cancel option)
5. Incoming transfer banner at top of modal (recipient view with accept/decline)
6. Handle accept/decline/cancel actions
7. Post-acceptance: UI updates to reflect new owner

### Phase 6: Polish
1. Animations and transitions
2. Mobile responsive adjustments
3. Accessibility audit

## Testing Checklist

### Visual
- [ ] Modal matches Minimal Light design
- [ ] Correct icons and colors for access levels
- [ ] Hover and focus states work
- [ ] Animations are smooth

### Functional
- [ ] Can add member with correct access level (owner/manager only)
- [ ] Can change existing member's access level
- [ ] Can remove member
- [ ] Owner can manage all members
- [ ] Manager can only manage writers/readers
- [ ] Manager cannot modify other managers or owner
- [ ] Writer/reader see read-only view
- [ ] Writer/reader can view all members

### Transfer Flow
- [ ] Owner sees "Transfer ownership" in member overflow menu
- [ ] Non-owners do not see "Transfer ownership" option
- [ ] Clicking "Transfer ownership" opens confirmation modal with member name
- [ ] Cannot request transfer if one is already pending (option disabled or hidden)
- [ ] Pending transfer shows inline banner on recipient's member row
- [ ] Owner can cancel pending transfer from inline banner
- [ ] Recipient sees incoming transfer banner at top of share modal
- [ ] Recipient can accept transfer (becomes owner)
- [ ] Recipient can decline (delete) transfer
- [ ] After acceptance: recipient is owner, former owner is manager
- [ ] Deleted transfers are hard deleted (not visible after delete)
- [ ] UI correctly refetches memberships after transfer acceptance

### Edge Cases
- [ ] Empty state displays correctly
- [ ] Long user IDs truncate properly
- [ ] Invalid user ID shows error
- [ ] Network error shows retry option
- [ ] User can't share with themselves
- [ ] Pending transfer UI updates after accept/delete

### Accessibility
- [ ] Keyboard navigation works
- [ ] Screen reader announces actions
- [ ] Focus management in modal
- [ ] Color contrast meets WCAG AA

## API Client Updates

The generated TypeScript client needs these services:

```typescript
// Membership operations
SharingService.listConversationMemberships({ conversationId })
SharingService.shareConversation({ conversationId, requestBody })
SharingService.updateConversationMembership({ conversationId, userId, requestBody })
SharingService.deleteConversationMembership({ conversationId, userId })

// Ownership transfer operations
SharingService.listPendingTransfers({ role? })        // GET /v1/ownership-transfers
SharingService.createOwnershipTransfer({ requestBody }) // POST /v1/ownership-transfers
SharingService.getTransfer({ transferId })            // GET /v1/ownership-transfers/{id}
SharingService.acceptTransfer({ transferId })         // POST /v1/ownership-transfers/{id}/accept (returns 204)
SharingService.deleteTransfer({ transferId })         // DELETE /v1/ownership-transfers/{id} (returns 204)
```

**Note**: `acceptTransfer` returns `204 No Content` on success (the transfer is deleted after ownership changes). The UI should refetch memberships after acceptance to reflect the new owner.

Verify the client is regenerated with sharing endpoints included.

## References

- Sharing API: [Enhancement 024](./024-sharing-api-enhancements.md)
- Design system: [Enhancement 022](./022-chat-app-design.md)
- Implementation patterns: [Enhancement 023](./023-chat-app-implementation.md)
- API Spec: `memory-service-contracts/src/main/resources/openapi.yml`
- Current implementation: `frontends/chat-frontend/src/`
