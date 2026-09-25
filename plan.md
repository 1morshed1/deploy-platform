## Project 2: Automated Multi-Tenant SaaS Deployment Platform (Mini-Vercel/Heroku)

### Concept & Why It Matters

You are building a simplified Platform-as-a-Service. Users submit a GitHub repo URL, your system detects the language/framework, builds a container image, deploys it to Kubernetes, assigns it a unique subdomain, and shows them monitoring dashboards. This is a subset of what Vercel, Heroku, Railway, and Render do.

This project is heavily weighted toward DevOps and Kubernetes internals. It demonstrates that you understand not just how to deploy to Kubernetes, but how to programmatically manage Kubernetes resources — which is a much rarer and more valuable skill.

### Architecture Diagram

```
  User                                                                          
   │                                                                          
   │  POST /api/v1/deploy                                                     
   │  { repo_url, branch }                                                    
   ▼                                                                          
┌──────────────────────────────────────────────────────────────────────────────┐
│                        KUBERNETES CLUSTER                                    │
│                                                                              │
│  ┌────────────┐     ┌──────────────┐     ┌────────────────┐                  │
│  │  Go API    │────▶│  Build Queue │────▶│  Builder Pod   │                  │
│  │  Server    │     │  (Redis +    │     │  (Docker-in-   │                  │
│  │            │     │   Asynq)     │     │   Docker or    │                  │
│  │            │     │              │     │   Kaniko)      │                  │
│  └────────────┘     └──────────────┘     └───────┬────────┘                  │
│       │                                          │                           │
│       │                                          │  Push image               │
│       │                                          ▼                           │
│       │                                   ┌─────────────┐                    │
│       │                                   │   AWS ECR    │                   │
│       │                                   │  (Container  │                   │
│       │                                   │   Registry)  │                   │
│       │                                   └──────┬──────┘                    │
│       │                                          │                           │
│       │  Programmatic kubectl                    │  Pull image               │
│       ▼                                          ▼                           │
│  ┌──────────────────────────────────────────────────────┐                     │
│  │              Tenant Namespace                        │                     │
│  │  ┌────────────┐  ┌───────────┐  ┌─────────────┐     │                     │
│  │  │ Deployment │  │  Service  │  │   Ingress   │     │                     │
│  │  │ (user app) │  │ (ClusterIP│  │  (subdomain │     │                     │
│  │  │            │  │   :80)    │  │   routing)  │     │                     │
│  │  └────────────┘  └───────────┘  └─────────────┘     │                     │
│  └──────────────────────────────────────────────────────┘                     │
│                                                                              │
│  ┌──────────────────────────────────────────────────┐                        │
│  │  Observability                                    │                       │
│  │  Prometheus ──▶ Grafana (per-tenant dashboards)   │                       │
│  │  Jaeger (build pipeline tracing)                  │                       │
│  │  Loki (build logs + app logs)                     │                       │
│  └──────────────────────────────────────────────────┘                        │
│                                                                              │
│  ┌──────────────────────────────────────────────────┐                        │
│  │  Platform Operator (Custom Controller)            │                       │
│  │  Watches: TenantApp CRD                           │                       │
│  │  Reconciles: Deployment + Service + Ingress       │                       │
│  └──────────────────────────────────────────────────┘                        │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Tech Stack Breakdown

| Layer | Technology | Why This Choice |
|---|---|---|
| API Server | Go + Gin | Same reasons as Project 1. Additionally, the official Kubernetes client library (`client-go`) is written in Go, so interacting with K8s from Go is first-class. |
| Build Queue | Redis + Asynq | Builds are slow (30-120 seconds). They must be asynchronous. Asynq gives you retry logic, priority queues, and a built-in monitoring UI. |
| Container Building | Kaniko (preferred) or Docker-in-Docker | Kaniko builds Docker images inside a Kubernetes pod without requiring a Docker daemon. This is more secure than Docker-in-Docker, which requires privileged containers. |
| Container Registry | AWS ECR | Private registry, integrates natively with EKS. Each tenant's image is tagged with `{tenant_id}:{git_sha}`. |
| Orchestration | Kubernetes + Custom Resource Definition (CRD) | Instead of manually generating YAML, define a `TenantApp` CRD and write a custom controller that reconciles it into the correct Deployment + Service + Ingress. This is the advanced approach. |
| Ingress | Nginx Ingress Controller + Wildcard DNS | Route `{app-name}.yourdomain.com` to the correct Service. Use Let's Encrypt (cert-manager) for automatic HTTPS. |
| Monitoring | Prometheus + Grafana | Prometheus scrapes metrics from every tenant pod. Grafana dashboards are provisioned per-tenant using Grafana's API. |
| Infrastructure | Terraform | Same as Project 1. Provisions EKS, ECR, Route53 (DNS), and the Nginx Ingress Controller. |

### Phase-by-Phase Build Plan

#### Phase 1: The API and Build Queue

**Database Schema (PostgreSQL):**

```sql
CREATE TABLE tenants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email       VARCHAR(255) UNIQUE NOT NULL,
    api_key     VARCHAR(64) UNIQUE NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE deployments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    repo_url        VARCHAR(512) NOT NULL,
    branch          VARCHAR(128) NOT NULL DEFAULT 'main',
    detected_lang   VARCHAR(32),                           -- go, node, python, static
    build_status    VARCHAR(20) NOT NULL DEFAULT 'queued', -- queued, building, built, deploying, live, failed
    image_uri       VARCHAR(512),                          -- ECR URI once built
    subdomain       VARCHAR(128) UNIQUE,                   -- {app-name}.yourdomain.com
    replicas        INT NOT NULL DEFAULT 1,
    cpu_limit       VARCHAR(10) NOT NULL DEFAULT '250m',
    memory_limit    VARCHAR(10) NOT NULL DEFAULT '256Mi',
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE build_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id   UUID NOT NULL REFERENCES deployments(id),
    log_line        TEXT NOT NULL,
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

**API Endpoints:**

- `POST /api/v1/deploy` — Accepts `{ repo_url, branch, app_name }`. Validates the repo is accessible (HEAD request to GitHub API). Detects language by checking for `go.mod`, `package.json`, `requirements.txt`, or `index.html` at the repo root. Enqueues a build task via Asynq. Returns deployment ID.
- `GET /api/v1/deployments/:id` — Returns current status (queued, building, live, failed) and the assigned subdomain.
- `GET /api/v1/deployments/:id/logs` — Streams build logs via Server-Sent Events (SSE). The user sees their build output in real-time.
- `DELETE /api/v1/deployments/:id` — Tears down the K8s resources and deletes the ECR image.
- `GET /api/v1/deployments` — Lists all deployments for the authenticated tenant.

**Language Detection Logic:**

```go
func detectLanguage(repoURL, branch string) (string, error) {
    // Use GitHub API to check for marker files at repo root
    // GET https://api.github.com/repos/{owner}/{repo}/contents/?ref={branch}
    files := fetchRepoRoot(repoURL, branch)

    switch {
    case contains(files, "go.mod"):
        return "go", nil
    case contains(files, "package.json"):
        return "node", nil
    case contains(files, "requirements.txt"), contains(files, "Pipfile"):
        return "python", nil
    case contains(files, "index.html"):
        return "static", nil
    default:
        return "", fmt.Errorf("unable to detect project language")
    }
}
```

#### Phase 2: The Builder (Kaniko)

When the Asynq worker picks up a build task, it creates a Kubernetes Job that runs Kaniko:

**Multi-Stage Dockerfile Templates (generated per language):**

For a Node.js app:
```dockerfile
# Build stage
FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --production=false
COPY . .
RUN npm run build

# Production stage
FROM node:20-alpine
WORKDIR /app
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/dist ./dist
COPY --from=builder /app/package.json .
EXPOSE 3000
CMD ["node", "dist/index.js"]
```

For a Go app:
```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /server
EXPOSE 8080
CMD ["/server"]
```

**Kaniko Job Manifest (generated programmatically by Go):**

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: build-{deployment-id}
  namespace: platform-builds
spec:
  ttlSecondsAfterFinished: 300
  template:
    spec:
      containers:
        - name: kaniko
          image: gcr.io/kaniko-project/executor:latest
          args:
            - "--dockerfile=Dockerfile"
            - "--context=git://github.com/{owner}/{repo}.git#refs/heads/{branch}"
            - "--destination={ecr-uri}:{tenant-id}-{git-sha}"
            - "--cache=true"
            - "--cache-repo={ecr-uri}/cache"
          volumeMounts:
            - name: docker-config
              mountPath: /kaniko/.docker
      restartPolicy: Never
      volumes:
        - name: docker-config
          secret:
            secretName: ecr-credentials
  backoffLimit: 2
```

Your Go backend watches this Job's status using `client-go`'s informer/watch mechanism. When the Job completes, it updates the deployment status to `built` and moves to Phase 3.

#### Phase 3: The Orchestrator (Kubernetes CRD + Custom Controller)

This is the most advanced part. Instead of generating raw YAML and running `kubectl apply`, you define a Custom Resource:

**TenantApp CRD:**

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: tenantapps.platform.yourdomain.com
spec:
  group: platform.yourdomain.com
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                imageUri:
                  type: string
                subdomain:
                  type: string
                replicas:
                  type: integer
                cpuLimit:
                  type: string
                memoryLimit:
                  type: string
  scope: Namespaced
  names:
    plural: tenantapps
    singular: tenantapp
    kind: TenantApp
```

Your Go custom controller (built using `controller-runtime` or `kubebuilder`) watches for `TenantApp` resources. When one is created, the controller's reconcile loop creates:
1. A **Namespace** for the tenant (if it does not exist).
2. A **Deployment** running the built image with the specified replicas and resource limits.
3. A **Service** (ClusterIP) pointing to the Deployment.
4. An **Ingress** rule mapping `{subdomain}.yourdomain.com` to the Service.
5. A **NetworkPolicy** isolating the tenant's namespace (tenants cannot talk to each other).
6. A **ResourceQuota** limiting the tenant's total CPU and memory usage.

#### Phase 4: Monitoring Per Tenant

Set up Prometheus with the Prometheus Operator (kube-prometheus-stack Helm chart). Each tenant's pod is automatically discovered via Prometheus's Kubernetes service discovery.

Create a Grafana dashboard template that shows:
- CPU usage over time for the tenant's pods.
- Memory usage and OOM kill events.
- HTTP request rate (if the tenant's app exports Prometheus metrics).
- Pod restart count.

Use Grafana's provisioning API to automatically create a dashboard for each tenant when their app is deployed. The dashboard URL is stored in the `deployments` table and returned via the API.

#### Phase 5: Tenant Isolation & Security

This is what separates a demo from a production system:

- **Namespace isolation:** Each tenant runs in their own namespace. NetworkPolicies prevent cross-namespace traffic.
- **ResourceQuotas:** Prevent a single tenant from consuming all cluster resources. Default: 500m CPU, 512Mi memory per tenant.
- **Pod Security Standards:** Enforce the `restricted` Pod Security Standard. No privileged containers, no host networking, no root users.
- **Image scanning:** Run Trivy on every built image before deploying. Reject images with critical CVEs.
- **Rate limiting:** The API rate-limits deploy requests to 5 per hour per tenant (use Redis-based rate limiting).

### Observability Strategy

Same three pillars as Project 1, plus:

- **Build pipeline tracing:** Each build gets a trace ID. The trace spans from the API request through the Asynq task, the Kaniko job, the ECR push, and the K8s resource creation. When a build fails, you can see exactly which step failed and how long each step took.
- **Build log streaming:** Build logs are captured from the Kaniko pod's stdout and streamed to the user via SSE. They are also stored in the `build_logs` table for debugging.

### Cost Management

- Use **kind** (Kubernetes in Docker) for local development. It runs a full K8s cluster inside Docker containers on your laptop.
- For cloud testing, use a single **t3.medium** node in EKS ($0.0416/hr, about $30/month).
- Use ECR's **lifecycle policies** to automatically delete images older than 30 days.
- Set a hard limit of 3 concurrent deployments in your dev environment.

---
---