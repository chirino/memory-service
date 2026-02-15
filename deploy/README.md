# Deploy

Deployment configuration and infrastructure resources for running the Memory Service.

## Contents

- **docker/** - Dockerfiles for utility images (e.g., context inspection)
- **keycloak/** - Keycloak realm configuration and database init scripts for OIDC/authentication
- **kustomize/** - Kustomize overlays for Kubernetes deployment with composable components
- **localstack/** - LocalStack init scripts for local S3-compatible storage
- **kind-config.yaml** - Kind cluster configuration for local Kubernetes development
