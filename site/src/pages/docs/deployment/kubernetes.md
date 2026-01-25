---
layout: ../../../layouts/DocsLayout.astro
title: Kubernetes Deployment
description: Deploy Memory Service on Kubernetes.
---

Deploy Memory Service on Kubernetes for scalable, production-grade deployments.

## Quick Start

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: memory-service
  labels:
    app: memory-service
spec:
  replicas: 2
  selector:
    matchLabels:
      app: memory-service
  template:
    metadata:
      labels:
        app: memory-service
    spec:
      containers:
        - name: memory-service
          image: ghcr.io/chirino/memory-service:1.0.0
          ports:
            - containerPort: 8080
              name: http
            - containerPort: 9000
              name: grpc
          env:
            - name: QUARKUS_DATASOURCE_JDBC_URL
              valueFrom:
                secretKeyRef:
                  name: memory-service-secrets
                  key: database-url
            - name: QUARKUS_DATASOURCE_USERNAME
              valueFrom:
                secretKeyRef:
                  name: memory-service-secrets
                  key: database-username
            - name: QUARKUS_DATASOURCE_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: memory-service-secrets
                  key: database-password
          resources:
            requests:
              memory: "512Mi"
              cpu: "250m"
            limits:
              memory: "1Gi"
              cpu: "1000m"
          livenessProbe:
            httpGet:
              path: /q/health/live
              port: 8080
            initialDelaySeconds: 30
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /q/health/ready
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 5
```

### Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: memory-service
spec:
  selector:
    app: memory-service
  ports:
    - name: http
      port: 8080
      targetPort: 8080
    - name: grpc
      port: 9000
      targetPort: 9000
```

### Secrets

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: memory-service-secrets
type: Opaque
stringData:
  database-url: jdbc:postgresql://postgres:5432/memoryservice
  database-username: postgres
  database-password: your-secure-password
```

## ConfigMap for Settings

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: memory-service-config
data:
  application.properties: |
    # Logging
    quarkus.log.level=INFO

    # Health checks
    quarkus.health.extensions.enabled=true

    # Metrics
    quarkus.micrometer.export.prometheus.enabled=true
```

Mount the ConfigMap:

```yaml
spec:
  containers:
    - name: memory-service
      volumeMounts:
        - name: config
          mountPath: /deployments/config
  volumes:
    - name: config
      configMap:
        name: memory-service-config
```

## Ingress

### NGINX Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: memory-service
  annotations:
    nginx.ingress.kubernetes.io/backend-protocol: "HTTP"
spec:
  rules:
    - host: memory-service.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: memory-service
                port:
                  number: 8080
```

### gRPC Ingress

For gRPC traffic:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: memory-service-grpc
  annotations:
    nginx.ingress.kubernetes.io/backend-protocol: "GRPC"
spec:
  rules:
    - host: grpc.memory-service.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: memory-service
                port:
                  number: 9000
```

## Horizontal Pod Autoscaler

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: memory-service
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: memory-service
  minReplicas: 2
  maxReplicas: 10
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 70
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: 80
```

## Database (PostgreSQL)

Deploy PostgreSQL with pgvector:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres
spec:
  serviceName: postgres
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
        - name: postgres
          image: pgvector/pgvector:pg16
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRES_DB
              value: memoryservice
            - name: POSTGRES_USER
              valueFrom:
                secretKeyRef:
                  name: memory-service-secrets
                  key: database-username
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: memory-service-secrets
                  key: database-password
          volumeMounts:
            - name: postgres-data
              mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
    - metadata:
        name: postgres-data
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 10Gi
---
apiVersion: v1
kind: Service
metadata:
  name: postgres
spec:
  selector:
    app: postgres
  ports:
    - port: 5432
      targetPort: 5432
```

## Monitoring

### ServiceMonitor (Prometheus)

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: memory-service
spec:
  selector:
    matchLabels:
      app: memory-service
  endpoints:
    - port: http
      path: /q/metrics
      interval: 30s
```

## Network Policies

Restrict network access:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: memory-service
spec:
  podSelector:
    matchLabels:
      app: memory-service
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              access: memory-service
      ports:
        - port: 8080
        - port: 9000
  egress:
    - to:
        - podSelector:
            matchLabels:
              app: postgres
      ports:
        - port: 5432
```

## Helm Chart

A Helm chart is available for easier deployment:

```bash
helm repo add memory-service https://chirino.github.io/memory-service/charts
helm install my-memory-service memory-service/memory-service \
  --set database.url=jdbc:postgresql://postgres:5432/memoryservice \
  --set database.username=postgres \
  --set database.password=secret
```

## Next Steps

- Configure [Database Setup](/docs/deployment/databases/)
- Learn about [Configuration](/docs/configuration/)
