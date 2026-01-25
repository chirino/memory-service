---
layout: ../../layouts/DocsLayout.astro
title: Getting Started
description: Learn how to deploy Memory Service using Docker Compose for quick setup and testing.
---

This guide will walk you through deploying Memory Service using Docker Compose for a quick demo setup.

## Prerequisites

Before you begin, make sure you have:

- **Docker** and **Docker Compose** installed
- An **OpenAI API key** (or compatible endpoint)
- At least 4GB of available RAM

## Quick Start

### 1. Clone the Repository

```bash
git clone https://github.com/chirino/memory-service.git
cd memory-service
```

### 2. Set Up Environment

Create a `.env` file with your OpenAI API key:

```bash
echo "OPENAI_API_KEY=your-api-key-here" > .env
```

### 3. Deploy with Docker Compose

```bash
docker compose up -d
```

This will start:
- **Memory Service** on port 8080
- **PostgreSQL** for data storage
- **Redis** for caching
- **MongoDB** for vector storage
- **Keycloak** for authentication

### 4. Access the Application

Open `http://localhost:8080` in your browser and sign in with:
- Username: `bob`
- Password: `bob`

## Services Overview

| Service | Port | Description |
|---------|------|-------------|
| Memory Service | 8080 | Main API and web interface |
| PostgreSQL | 5432 | Primary database |
| Redis | 6379 | Caching layer |
| MongoDB | 27017 | Vector storage for semantic search |
| Keycloak | 8180 | OIDC authentication provider |

## Default Configuration

The Docker Compose setup includes:
- Pre-configured Keycloak realm with test users
- Automatic database initialization
- Health checks for all services
- Persistent data volumes

## Test Users

Keycloak is pre-configured with these test users:

| Username | Password | Role |
|----------|----------|------|
| bob | bob | user |
| alice | alice | user |
| charlie | charlie | user |

## Next Steps

- Learn about [Configuration Options](/docs/configuration/)
- Understand [Core Concepts](/docs/concepts/conversations/)
- Explore [Framework Integrations](/docs/apis/frameworks/quarkus/)
- Review [Deployment Options](/docs/deployment/kubernetes/)

## Troubleshooting

### Services Not Starting

Check service logs:
```bash
docker compose logs [service-name]
```

### Memory Issues

Increase Docker memory limits or reduce service memory usage in `docker-compose.yml`.

### Port Conflicts

Ensure ports 8080, 5432, 6379, 27017, and 8180 are available on your system.