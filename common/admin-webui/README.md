# Memory Service Admin Frontend

Administrative frontend for the Memory Service, providing admin and auditor access to conversations, entries, and system management.

## Project Structure

```
common/admin-webui/
├── mockups/                     # Phase 1: Static HTML mockups
│   ├── dashboard.html           # Main dashboard
│   ├── conversations.html       # Conversation list with filters
│   ├── conversation-detail.html # Single conversation view
│   ├── search.html              # Semantic search interface
│   └── eviction.html            # Eviction job management
├── src/                         # Future: JavaScript/React source
└── README.md                    # This file
```

## Phase 1: Static Mockups

The `mockups/` directory contains self-contained HTML files styled with Tailwind CSS 4.0. These are for design iteration before implementing interactive functionality.

### Viewing Mockups

Open any HTML file directly in a browser:

```bash
open mockups/dashboard.html
```

Or serve them locally:

```bash
npx serve mockups
```

### Design Specifications

See [docs/enhancements/020-admin-frontend.md](../../docs/enhancements/020-admin-frontend.md) for the full design document.

## Target API

The frontend consumes the Admin API defined in:
- `memory-service-contracts/src/main/resources/openapi-admin.yml`

## Roles

| Role | Access |
|------|--------|
| Admin | Full access: view, delete, restore, evict |
| Auditor | Read-only: view, search |
