---
layout: ../../layouts/DocsLayout.astro
title: Getting Started
description: Learn how to deploy Memory Service using Docker Compose for quick setup and testing.
---

This guide will walk you through deploying Memory Service using Docker Compose for a quick demo setup.

> **Note:** This project is currently in the proof-of-concept (POC) phase and has not yet published any releases. To try it out, you'll need to build it from source code. Don't worry—Docker Compose will handle building the project automatically when you run the deployment commands below. Be aware that the initial build may take several minutes, so please be patient.

## Prerequisites

Before you begin, make sure you have:

- **Docker** and **Docker Compose** installed
- An **OpenAI API key** (or compatible endpoint)

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
docker compose build
docker compose up -d
```

This will start:
- **Demo Chat App** for an AI chat interface
- **Memory Service** this project's service (used by the demo chat app)
- **Keycloak** for authentication (used by the memory service and demo chat app)
- **PostgreSQL** for data and vector storage (used by the memory service)
- **Redis** for caching (used by the memory service)
- **Prometheus** for metrics collection
- **Grafana** for metrics dashboards

### 4. Access the Demo Chat App

Open `http://localhost:8080` in your browser and sign in with:
- Username: `bob`
- Password: `bob`

## Test Users

Keycloak is pre-configured with these test users:

| Username | Password | Role |
|----------|----------|------|
| bob | bob | user |
| alice | alice | user, admin |
| charlie | charlie | user |

## Things to notice in the Demo.

* You can fork any user entry and switch between forks
* Agent memory stays consistent with the fork you're on.  Ask it to recall previous fact you have told it.
* Streaming responses survive browser page reloads.  You can even switch to a different device and still view the response that is currently being generated.
* Users can see a list of all their previous conversations.

## Next Steps

- Understand [Core Concepts](/docs/concepts/)
- Explore Developer Guides for
  * [Quarkus Langchain4j](/docs/quarkus/)
  * [Spring AI](/docs/spring/)
- Learn about [Server Configuration Options](/docs/configuration/)
