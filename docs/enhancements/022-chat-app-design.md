# Enhancement 022: Chat App Frontend Design

**Status**: ✅ Complete

## Overview

This document outlines the design and implementation plan for creating polished, high-quality mockups of the AI chat application. The current implementation in `frontends/chat-frontend/` is functional but lacks visual polish. This effort will explore distinctive design directions through style mockups, then produce a final single-page HTML mockup using the chosen style.

## Outcome

**Chosen Style**: Minimal Light

The Minimal Light style was selected for its clean, warm aesthetic that emphasizes readability and avoids generic AI chat patterns. Key characteristics:
- Warm cream palette (`#FDFBF7`) with ink (`#1a1a1a`) text
- Typography: Instrument Serif (headings) + DM Sans (body)
- Accent colors: Sage (`#7d9a8c`) for success/active states, Terracotta (`#c67a5c`) for warnings/delete
- Subtle shadows and rounded corners for depth
- Elegant animations and hover states

**Deliverables**:
- `frontends/chat-frontend/style-mockups/minimal-light.html` - Chosen style
- `frontends/chat-frontend/style-mockups/dark-mode-pro.html` - Alternative dark theme
- `frontends/chat-frontend/style-mockups/terminal.html` - Alternative terminal theme
- `frontends/chat-frontend/mockups/chat.html` - Complete mockup with all UI states
- `frontends/chat-frontend/mockups/chat-empty.html` - New conversation/empty state

**Next Steps**: See [Enhancement 023: Chat App Implementation](./023-chat-app-implementation.md)

## Goals

1. Create visually distinctive mockups that avoid generic AI chat aesthetics
2. Explore multiple style variations before committing to a direction
3. Produce a single-page HTML mockup that can serve as a design reference
4. Maintain all current functionality while elevating the visual design

## Current App Analysis

The existing chat app has a two-panel layout with the following functional areas:

### Left Sidebar (Conversation Browser)
- **Header**: "Conversations" title with "New chat" button
- **Search**: Real-time filtering by title and message preview
- **Conversation List**: Shows up to 20 conversations sorted by most recent
  - Each item displays: title (or "Untitled conversation"), last updated timestamp
  - Selected state highlighting
  - Resume indicator (spinner) for incomplete streams
- **Hover Actions**: Delete and Index buttons appear on hover

### Right Panel (Chat Interface)
- **Header**: "Chat with your agent" title with stream mode toggle (SSE/WebSocket)
- **Empty State**: Card prompting user to start chatting
- **Messages Viewport**: Scrollable area with conversation turns
  - User messages: Right-aligned, primary color
  - Assistant messages: Left-aligned, muted background
  - Markdown rendering support
  - Edit button on hover for user messages
  - Fork menu showing branch options
- **Composer**: Multi-line textarea with Send/Stop button

## Core User Activities

### 1. Conversation Management
- Create new conversations
- Browse and search existing conversations
- Select and switch between conversations
- Delete unwanted conversations
- Index conversations for search

### 2. Messaging
- Send messages to the AI agent
- View streaming responses in real-time
- Stop ongoing responses
- View message history with markdown formatting

### 3. Conversation Forking
- Edit any user message to create a branch
- View and navigate between forks
- See fork points indicated in the message flow

### 4. Session Resumption
- Automatic detection of incomplete streams
- Resume button/indicator for resumable conversations
- Seamless continuation of interrupted sessions

## Screen Inventory

### Primary View
| Component | Description |
|-----------|-------------|
| Conversation Sidebar | Fixed-width panel with search and conversation list |
| Chat Panel | Flexible main area with messages and composer |

### UI States
| State | Description |
|-------|-------------|
| Empty | No conversation selected, prompting user to start |
| Active | Conversation loaded with message history |
| Streaming | AI response being received in real-time |
| Editing | User editing a message to create a fork |

### Overlay Components
| Component | Description |
|-----------|-------------|
| Fork Menu | Dropdown showing available branches |
| Hover Actions | Delete/Index buttons on conversation items |
| Confirmation Dialogs | Delete confirmation |

## Design Principles

### Visual Design
- **Distinctive**: Avoid generic chat UI patterns; create a memorable aesthetic
- **Clean information hierarchy**: Messages are the focus, chrome is minimal
- **Smooth interactions**: Transitions and hover states feel polished
- **Conversation flow**: Clear visual separation between turns

### Layout
- Two-panel split: fixed sidebar + flexible chat area
- Full-height layout (viewport-filling)
- Responsive message widths with max-width constraints
- Consistent spacing and alignment

### Message Styling
- Clear differentiation between user and assistant messages
- Support for code blocks, lists, and other markdown elements
- Subtle visual cues for message state (streaming, pending)
- Fork indicators that don't disrupt reading flow

## Phase 0.5: Style Exploration ✅

Before building the full mockup, we explore different visual styles using the chat interface as a representative sample. This allows stakeholders to evaluate and choose a design direction.

### Objectives
- ✅ Generate 3 distinct visual styles for comparison
- ✅ Explore different color palettes, message bubble styles, and layouts
- ✅ Get stakeholder buy-in on visual direction before final mockup

### Style Mockups

All style mockups are placed in `frontends/chat-frontend/style-mockups/` and implement the same chat interface with different aesthetics:

| Style | File | Description |
|-------|------|-------------|
| **Minimal Light** ⭐ | `minimal-light.html` | Clean whitespace-focused design with warm cream palette, Instrument Serif + DM Sans typography, sage and terracotta accents. **Selected as the final style.** |
| **Dark Mode Pro** | `dark-mode-pro.html` | Rich dark theme with Syne + Outfit fonts, gradient accents, glassmorphism, and violet/rose color palette. |
| **Terminal** | `terminal.html` | Terminal-inspired dark theme with JetBrains Mono typography, mint green accents (#48d597), and command-line aesthetic. |

### Evaluation Criteria

When reviewing style mockups, consider:
- **Readability**: Is text easy to read in both user and assistant messages?
- **Distinctiveness**: Does it stand out from generic chat UIs?
- **Code Block Rendering**: Do code snippets look good?
- **Information Density**: Does it show enough without feeling cluttered?
- **Scalability**: Can this style work for long conversations?

## Phase 1: Static HTML Mockup ✅

### Objectives
- ✅ Create a single self-contained HTML page styled with Tailwind CSS 4.0
- ✅ Apply the chosen style from Phase 0.5 consistently
- ✅ Include all UI states and interactions as visible elements
- ✅ Establish design patterns for the final implementation

### Technology Stack
- **Tailwind CSS 4.0** via CDN
- **Heroicons** for iconography (SVG inline)
- Self-contained HTML file (no build step required)

### CDN Resources
```html
<!-- Tailwind CSS 4.0 -->
<script src="https://cdn.tailwindcss.com"></script>

<!-- Optional: Inter font for cleaner typography -->
<link rel="stylesheet" href="https://rsms.me/inter/inter.css">
```

### File Structure
```
frontends/chat-frontend/
├── style-mockups/                    # Phase 0.5: Style exploration
│   ├── minimal-light.html            # Clean, whitespace-focused (chosen)
│   ├── dark-mode-pro.html            # Dark theme, gradients
│   └── terminal.html                 # Terminal-inspired
├── mockups/                          # Phase 1: Final mockups
│   ├── chat.html                     # Complete chat interface with messages
│   └── chat-empty.html               # New conversation / empty state
├── src/                              # Current React implementation
└── README.md
```

### Mockup Specifications

#### chat.html

**Overall Layout**:
- Full viewport height (h-screen)
- Two-column layout: 320px sidebar + flexible chat area
- Border separator between panels

**Sidebar Section**:
- Header with:
  - "Conversations" title
  - "New chat" button (primary style)
- Search input field with placeholder
- Conversation list:
  - 5-7 sample conversations with varied titles
  - One marked as selected (highlighted)
  - One showing resume indicator (spinner)
  - Hover state visible on one item showing delete/index buttons
- Timestamps showing relative time ("2 min ago", "Yesterday")

**Chat Panel Header**:
- "Chat with your agent" heading
- Stream mode toggle (SSE selected)
- Visual separation from message area

**Messages Area**:
- Sample conversation demonstrating various message types:
  - User message (short)
  - Assistant message with paragraph text
  - User message asking about code
  - Assistant message with code block (syntax highlighted appearance)
  - User message with edit button visible (hover state)
  - Fork indicator below a message showing "3 forks" link
- Sticky user message header with gradient fade
- One message showing streaming state (partial text, animated cursor)

**Composer Section**:
- Multi-line textarea (3 rows)
- Placeholder text: "Type a message..."
- Send button (enabled state)
- Alternative: Show "Stop" button during streaming state

**Empty State Variant**:
- Can be shown as an alternative section
- Card with icon, title "No messages yet"
- Subtitle prompting to start chatting

**Fork Menu Overlay**:
- Positioned below a message (can show as separate component)
- Lists 2-3 fork options with:
  - Fork label (user message preview)
  - Timestamp
  - "Active" badge on current fork
  - Parent conversation option if applicable

**Edit Mode Overlay**:
- Shows a user message replaced with textarea
- Original message text in editable field
- Save and Cancel buttons

## Data Models Reference

Key data structures for mockup sample data:

### Conversation
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "title": "Help with React hooks",
  "createdAt": "2025-01-28T10:30:00Z",
  "updatedAt": "2025-01-29T14:22:00Z"
}
```

### Message Entry
```json
{
  "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "conversationId": "550e8400-e29b-41d4-a716-446655440000",
  "role": "user",
  "content": "How do I use useEffect for data fetching?",
  "createdAt": "2025-01-29T14:20:00Z"
}
```

### Fork Summary
```json
{
  "conversationId": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "forkedAtEntryId": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "title": "Using async/await instead",
  "createdAt": "2025-01-29T14:25:00Z"
}
```

## Sample Content for Mockups

### Conversation List
1. "Help with React hooks" - 2 min ago (selected)
2. "Python async patterns" - 1 hour ago
3. "Database schema design" - Yesterday
4. "API authentication" - 2 days ago (resumable indicator)
5. "CSS Grid layout" - 3 days ago

### Sample Messages

**User**: "How do I use useEffect for data fetching?"

**Assistant**: "Here's how to properly fetch data with useEffect:

```javascript
useEffect(() => {
  const fetchData = async () => {
    const response = await fetch('/api/data');
    const data = await response.json();
    setData(data);
  };

  fetchData();
}, []);
```

Key points to remember:
- Always handle cleanup for subscriptions
- Consider using a library like React Query for complex cases
- Handle loading and error states"

**User**: "What about error handling?"

**Assistant** (streaming): "Great question! You should wrap your fetch in a try-catch block..."

## Success Criteria

### Phase 0.5 (Style Exploration) ✅
1. ✅ Three distinct style mockups created
2. ✅ Each mockup shows the same functional elements
3. ✅ Styles are visually differentiated and production-quality
4. ✅ Stakeholder feedback collected and style chosen (Minimal Light)

### Phase 1 (Final Mockup) ✅
1. ✅ Single HTML file with complete chat interface
2. ✅ All UI states represented (empty, active, streaming, editing)
3. ✅ Responsive layout works on desktop and tablet
4. ✅ Hover and focus states included
5. ✅ Code blocks render attractively
6. ✅ Design approved for implementation reference

## Phase 2: Implementation

See [Enhancement 023: Chat App Implementation](./023-chat-app-implementation.md) for the implementation plan.

## References

- Current implementation: `frontends/chat-frontend/src/`
- API Spec: `memory-service-contracts/src/main/resources/openapi.yml`
- Tailwind CSS 4.0: https://tailwindcss.com/docs
- Admin Frontend Design: `docs/enhancements/020-admin-frontend.md`
