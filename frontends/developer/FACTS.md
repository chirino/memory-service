# Developer Frontend Facts

## Overview
Developer-oriented frontend for inspecting conversations and episodic memories across all users. Built with React 19, TypeScript, TanStack Router, and Tailwind CSS 4.0.

## Key Technologies
- **React 19** with TypeScript
- **TanStack Router** for file-based routing (v7+)
- **TanStack Query** for data fetching and caching
- **Tailwind CSS 4.0** with Minimal Light design system
- **Vite** as build tool
- **OIDC** authentication with role-based access

## Architecture Patterns

### Routing
- File-based routing using TanStack Router
- Routes live in `src/routes/`
- Root layout in `src/routes/__root.tsx` handles auth and layout
- Dynamic routes use `$param` syntax (e.g., `$conversationId.tsx`)

### Authentication
- OIDC integration in `src/lib/auth.tsx`
- Requires `admin` or `auditor` role
- Config loaded from `/config.json` at runtime
- `RequireAuth` wrapper in root layout enforces auth

### API Integration
- Custom React Query hooks in `src/hooks/useAdminApi.ts`
- Uses Admin API from `openapi-admin.yml`
- Fetch-based implementation with auth token injection
- Generated React Query helpers use typed object query keys from `@hey-api/openapi-ts`; do not replace them with string-array query keys.
- The React Query generator intentionally skips Admin SSE operations (`adminEvict`, `adminSubscribeEvents`) because those endpoints use `client.sse.*`; use the raw generated SDK for streaming calls.
- History attachment rendering uses stored `attachmentId` to build admin download-url endpoints; do not parse internal `/v1/attachments/{id}` IDs out of `href`, because stored history entries no longer include server-generated attachment hrefs.

### Design System
- Minimal Light palette: cream (#FAF8F5), sage (#8B9A8E), terracotta (#C17B6B)
- Design tokens in `tailwind.config.js`
- `src/index.css` imports Google Fonts; the integrated `/developer/*` CSP must allow `https://fonts.googleapis.com` in `style-src` and `https://fonts.gstatic.com` in `font-src`.
- Reusable UI components in `src/components/ui/`
- Consistent spacing and typography

## Component Structure

### Layout Components
- `src/components/layout/sidebar.tsx` - Fixed left navigation
- `src/routes/__root.tsx` - Root layout with auth and sidebar

### UI Components
- `src/components/ui/button.tsx` - Button with variants
- `src/components/ui/badge.tsx` - Status badges
- `src/components/error-boundary.tsx` - Error boundary wrapper

### Route Components
- `src/routes/conversations/index.tsx` - Conversation list with filters
- `src/routes/conversations/$conversationId.tsx` - Conversation detail with entry timeline
- `src/routes/memories/index.tsx` - Memory browser with namespace navigation
- `src/routes/memories/$memoryId.tsx` - Memory detail with full value display
- `src/routes/search.tsx` - Unified search across conversations and memories

## API Hooks

### Conversations
- `useAdminConversations(filters, options)` - List conversations with archive filter
- `useAdminConversation(id)` - Get single conversation
- `useAdminConversationEntries(id)` - Get conversation entries
- `useArchiveConversation()` - Archive mutation
- `useUnarchiveConversation()` - Unarchive mutation

### Memories
- `useAdminMemories(filters, options)` - List memories with namespace/key filters
- `useAdminMemory(id)` - Get single memory

## Key Features

### Conversations
- Table view with title, user, created date, entry count
- Archive filter: active/all/archived
- Archive/unarchive actions
- Detail view with metadata panel and entry timeline
- Entry cards show role, content, timestamps, metadata

### Memories
- Namespace hierarchy navigation in sidebar
- Archive filter and key prefix search
- Memory cards with namespace, key, value preview
- Detail view with full value, attributes, metadata
- Copy value to clipboard

### Search
- Unified search across conversations and memories
- Filter by type: all/conversations/memories
- Debounced search input (300ms)
- Client-side filtering on title, ID, namespace, key, content
- Results grouped by type with counts

## Configuration

### Runtime Config (`/config.json`)
```json
{
  "apiUrl": "http://localhost:8082",
  "oidc": {
    "authority": "http://localhost:8081/realms/memory-service",
    "clientId": "developer-frontend",
    "redirectUri": "http://localhost:3000/developer/"
  }
}
```


### Build Config
- Vite config in `vite.config.ts` with TanStack Router plugin; `src/routeTree.gen.ts` is generated and gitignored.
- `npm run build` runs Vite before `tsc -b` so the route tree exists before TypeScript checks run.
- TypeScript config in `tsconfig.json` with path aliases (`@/*`)
- Tailwind config in `tailwind.config.js` with design tokens
- ESLint config in `eslint.config.js`

## Development

### Setup
```bash
cd frontends/developer
npm install
npm run dev
```

### Build
```bash
npm run build
npm run preview
```

### Type Checking
```bash
npx tsc -b
```

## Design Decisions

### Why TanStack Router?
- File-based routing with type safety
- Better than React Router for new projects
- Integrated with TanStack Query

### Why Custom API Hooks?
- Immediate implementation without waiting for generated client
- Full control over caching and error handling
- Easy to migrate to generated client later

### Why Skip Real-Time Updates?
- Optional feature for initial implementation
- Can be added later with SSE or WebSocket
- Focus on core inspection features first

## Future Enhancements
- Real-time updates via SSE (Phase 5)
- Advanced filtering and sorting
- Bulk operations
- Export functionality
- Memory visualization
- Conversation analytics
