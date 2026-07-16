# Memory Service Developer Frontend

A developer-oriented frontend for inspecting conversations, entries, and episodic memories across all users in the Memory Service.

## Overview

This frontend provides admin and auditor users with tools to:

- **Browse Conversations**: View all conversations across users with detailed entry inspection
- **Explore Memories**: Navigate episodic memories by namespace hierarchy
- **Search**: Find conversations and memories quickly with semantic and attribute-based search
- **Monitor Processes**: View and inspect cognitive memory processing pipelines

## Technology Stack

- **React 19** - UI framework
- **Vite** - Build tool and dev server
- **TypeScript** - Type safety
- **Tailwind CSS 4.0** - Styling with Minimal Light design system
- **TanStack Router** - File-based routing
- **TanStack Query** - Data fetching and caching
- **react-oidc-context** - OIDC authentication
- **Radix UI** - Accessible UI primitives

## Getting Started

### Prerequisites

- Node.js 18+ and npm
- Memory Service running (default: `http://localhost:8082`)
- Cognitive Memory Service running (default: `http://localhost:8090`) - optional, for process monitoring
- Keycloak or compatible OIDC provider (default: `http://localhost:8080`)

### Installation

```bash
cd frontends/developer
npm install
```

### Configuration

Edit `public/config.json` to match your environment:

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

**Cognitive Memory Service Configuration:**

The cognitive service URL is configured at runtime via `public/config.json`:

```json
{
  "apiUrl": "http://localhost:8082",
  "cognitiveApiUrl": "",
  "oidc": { ... }
}
```

- **Development**: Leave `cognitiveApiUrl` empty. Vite dev server proxies `/api/processes/*` to `http://localhost:8090` automatically.
- **Production**: Set `cognitiveApiUrl` to your cognitive service URL (e.g., `"https://cognitive.example.com"`), or leave empty if behind the same reverse proxy.

**CORS Requirements (Production Only):**
When `cognitiveApiUrl` points to a different origin, the cognitive-memory service must allow CORS:
- `Access-Control-Allow-Origin: https://your-frontend-domain.com`
- `Access-Control-Allow-Methods: GET, OPTIONS`
- `Access-Control-Allow-Headers: Content-Type`

### Development

```bash
npm run dev
```

The app will be available at `http://localhost:3000`.

### Generate API Client

After updating `contracts/openapi/openapi-admin.yml`:

```bash
npm run generate
```

This regenerates TypeScript types and services in `src/client/`.

### Build for Production

```bash
npm run build
```

Output will be in `dist/`.

## Project Structure

```
frontends/developer/
├── src/
│   ├── routes/              # TanStack Router file-based routes
│   │   ├── __root.tsx       # Root layout with sidebar
│   │   ├── conversations/   # Conversation routes
│   │   ├── memories/        # Memory routes
│   │   ├── search/          # Search routes
│   │   └── processes.tsx    # Cognitive processes route
│   ├── components/
│   │   ├── layout/          # Layout components (sidebar, etc.)
│   │   ├── conversations/   # Conversation-specific components
│   │   ├── memories/        # Memory-specific components
│   │   ├── search/          # Search components
│   │   └── ui/              # Reusable UI primitives
│   ├── api/
│   │   ├── client.ts        # Memory Service API client
│   │   ├── cognitive-client.ts  # Cognitive Memory API client
│   │   └── generated/       # Generated API types
│   ├── hooks/               # Custom React hooks
│   └── lib/
│       ├── auth.tsx         # Authentication context
│       ├── config.ts        # Configuration loading
│       └── utils.ts         # Utility functions
├── public/
│   └── config.json          # Runtime configuration
└── package.json
```

## Authentication

The frontend requires admin or auditor role. Users without these roles will see an access denied screen.

### Role Permissions

| Feature | Admin | Auditor |
|---------|:-----:|:-------:|
| View conversations | ✅ | ✅ |
| View entries | ✅ | ✅ |
| View memories | ✅ | ✅ |
| Search | ✅ | ✅ |
| Monitor processes | ✅ | ✅ |
| Archive conversations | ✅ | ❌ |
| Delete memories | ✅ | ❌ |

## Development Workflow

### Adding a New Route

1. Create a new file in `src/routes/` (e.g., `src/routes/my-feature.tsx`)
2. TanStack Router will auto-generate the route
3. Add navigation link in `src/components/layout/sidebar.tsx`

### Adding a New Component

1. Create component in appropriate directory under `src/components/`
2. Export from component file
3. Import and use in routes or other components

### Working with the API

The API client is generated from `openapi-admin.yml`. After regenerating:

```typescript
import { ConversationsService } from '@/client';

// Use in React Query
const { data } = useQuery({
  queryKey: ['conversations'],
  queryFn: () => ConversationsService.adminListConversations({ limit: 20 }),
});
```

## Design System

The frontend uses the Minimal Light design system from `chat-frontend`:

- **Colors**: Cream background, ink text, sage accents, terracotta warnings
- **Typography**: Instrument Serif (headings), DM Sans (body), JetBrains Mono (code)
- **Components**: Consistent with `chat-frontend` patterns

## Scripts

- `npm run dev` - Start development server on port 3000
- `npm run build` - Build for production
- `npm run preview` - Preview production build
- `npm run generate` - Regenerate API client from OpenAPI spec
- `npm run lint` - Run ESLint
- `npm run prettier` - Format code with Prettier

## Troubleshooting

### TypeScript Errors

If you see TypeScript errors after creating new files, run:

```bash
npm install
```

The errors are expected until dependencies are installed.

### OIDC Configuration

Ensure your OIDC client is configured with:
- Redirect URI: `http://localhost:3000/developer/`
- Valid scopes: `openid profile email roles`
- Client type: Public (SPA)

### API Connection

Verify the Memory Service is running and accessible at the configured `apiUrl`.

## Contributing

This frontend follows the same patterns as `chat-frontend`. When adding features:

1. Reuse components and patterns from `chat-frontend` where applicable
2. Follow the Minimal Light design system
3. Use TanStack Query for data fetching
4. Keep routes focused and simple
5. Add loading states and error handling

## Related Documentation

- [Enhancement 106](../../docs/enhancements/implemented/106-developer-frontend-integration.md) - Full design document
- [Admin API Contract](../../contracts/openapi/openapi-admin.yml)
- [Chat Frontend](../chat-frontend/) - Reference implementation
