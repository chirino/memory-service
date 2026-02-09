# Memory Service Documentation Site

This directory contains the documentation site source for the memory service.

## Structure

```
site/
├── src/
│   └── pages/
│       └── docs/
│           ├── spring/
│           │   └── sharing.mdx      # Spring conversation sharing guide
│           └── quarkus/
│               └── sharing.mdx      # Quarkus conversation sharing guide
└── README.md
```

## Documentation Pages

### Spring Guides

- **sharing.mdx** - Comprehensive guide for implementing conversation sharing and ownership transfer in Spring Boot applications
  - Covers membership management (add, update, remove members)
  - Explains ownership transfer workflow
  - Provides working curl examples
  - Corresponds to checkpoint 05

### Quarkus Guides

- **sharing.mdx** - Comprehensive guide for implementing conversation sharing and ownership transfer in Quarkus applications
  - Same content as Spring guide but with Quarkus-specific code
  - Corresponds to checkpoint 05

## TODO: Site Infrastructure

The following items need to be completed when setting up the actual documentation site:

### 1. Create Site Build Configuration

- Set up Astro, Docusaurus, or similar static site generator
- Configure MDX support
- Set up build and deployment pipeline

### 2. Create Navigation Layout

Create `site/src/layouts/DocsLayout.astro` (or equivalent) with navigation structure:

```
Spring Boot
  - Getting Started
  - Basic Setup
  - Advanced Features
  - Conversation Sharing ← NEW

Quarkus
  - Getting Started
  - Basic Setup
  - Advanced Features
  - Conversation Sharing ← NEW
```

### 3. Create Missing Documentation Pages

The sharing guides reference these prerequisite guides that need to be created:

- `site/src/pages/docs/spring/advanced-features.mdx`
- `site/src/pages/docs/quarkus/advanced-features.mdx`
- `site/src/pages/docs/spring/getting-started.mdx`
- `site/src/pages/docs/quarkus/getting-started.mdx`

### 4. Update Advanced Features Guides

When creating the advanced-features.mdx pages, add a "Next Steps" section pointing to the sharing guide:

```markdown
## Next Steps

Now that you've learned about advanced features like forking and streaming, continue to [Conversation Sharing](./sharing) to learn how to:
- Share conversations with other users
- Manage access control with different permission levels
- Transfer conversation ownership
```

### 5. Add Cross-References

Link to these additional resources (to be created):
- API Reference documentation
- Security best practices guide
- Chat frontend integration guide

## Checkpoint References

The sharing guides correspond to:
- **Spring**: `spring/examples/doc-checkpoints/05-sharing/`
- **Quarkus**: `quarkus/examples/doc-checkpoints/05-sharing/`

These checkpoints contain working code examples that match the documentation.

## Contributing

When adding new documentation:
1. Create corresponding checkpoint code if applicable
2. Test all curl examples
3. Update navigation structure
4. Add cross-references between related guides
