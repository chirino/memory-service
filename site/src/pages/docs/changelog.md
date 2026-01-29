---
layout: ../../layouts/DocsLayout.astro
title: Changelog
description: Release history and changes for Memory Service
---

# Changelog

All notable changes to Memory Service are documented here.

This project follows [Semantic Versioning](https://semver.org/).

## Unreleased

Changes in the main branch that are not yet released.

### Added

- Initial documentation site with versioned docs support
- Astro-based static site with Tailwind CSS v4
- MDX support for interactive documentation

### Changed

- Documentation is now hosted on GitHub Pages

---

## Pre-release Development

Memory Service is currently in active development and has not yet reached version 1.0.0. APIs and features may change.

### Current Features

- **Conversation Storage** - Store and retrieve conversation history
- **Entry Management** - Full CRUD operations for entries
- **Conversation Forking** - Fork conversations at any entry
- **Semantic Search** - Vector-based search across conversations
- **Access Control** - User-based permissions and sharing
- **Multiple Databases** - Support for PostgreSQL and MongoDB
- **Caching** - Redis and Infinispan cache support
- **APIs** - REST and gRPC interfaces

### Planned Features

- Spring Boot starter support
- Additional vector store integrations
- Multi-agent conversation support

---

## Version Format

Version numbers follow the pattern `vX.Y.Z`:

- **X** (major): Breaking API changes
- **Y** (minor): New features, backward compatible
- **Z** (patch): Bug fixes, backward compatible

---

## How to Upgrade

When upgrading between versions:

1. Check this changelog for breaking changes
2. Update your dependencies
3. Run your test suite
4. Review any deprecated APIs

For detailed upgrade guides, see the relevant version's documentation.
