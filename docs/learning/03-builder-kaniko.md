# 03 — The builder: turning source into an image with Kaniko

**Code:** `internal/dockerfiles`, `internal/builder`, `cmd/worker`

The worker's job is to take "a GitHub repo + a detected language" and produce "a
container image in a registry." This is the slow, privileged heart of the
platform, and it's why the worker exists as a separate process (ADR 0002).

## Why Kaniko and not `docker build`?

The obvious way to build an image is to run `docker build`. But inside
Kubernetes that's a problem: `docker build` needs the Docker daemon, and giving a
pod access to the Docker daemon (Docker-in-Docker, or mounting the host socket)
means giving it **root on the node**. A malicious or buggy build could escape and
compromise the whole cluster.

**Kaniko** (`gcr.io/kaniko-project/executor`) solves this. It builds a Docker
image *from a Dockerfile* entirely in userspace, inside an unprivileged-ish
container, with **no Docker daemon**. It executes each Dockerfile instruction,
snapshots the filesystem, and pushes the result straight to a registry. That's
exactly what you want for untrusted, multi-tenant builds.

See [ADR 0007](../decisions/0007-kaniko-over-dind.md) for the full trade-off.

## The clever bit: we inject the Dockerfile

A real PaaS user doesn't write a Dockerfile — that's the platform's job. So:

1. `internal/github` detected the language (doc 02).
2. `internal/dockerfiles` holds a hand-tuned, multi-stage Dockerfile **template
   per language**, embedded into the binary with `go:embed`. `Render(lang)`
   returns the right one.
3. The builder puts that Dockerfile text into a **ConfigMap** and mounts it into
   the Kaniko pod at `/config/Dockerfile`.
4. Kaniko is told `--dockerfile=/config/Dockerfile` (our injected file) and
   `--context=git://github.com/owner/repo.git#refs/heads/branch` (the user's
   source). It clones the repo and builds it with *our* Dockerfile.

So the user's repo supplies the *code*; the platform supplies the *recipe*.

### Multi-stage Dockerfiles

Look at `internal/dockerfiles/go.Dockerfile`. It has two `FROM` stages: a
`builder` stage with the full Go toolchain, and a tiny `alpine` runtime stage
that receives only the compiled binary. The toolchain, source, and caches never
ship. This is why the final images are megabytes, not gigabytes — and it's a
technique worth learning on its own. Each template's comments explain its layer
caching (copy manifests → install deps → copy source), which makes rebuilds fast.

The one invariant to respect: **the `EXPOSE` port in each Dockerfile must match
`ContainerPort(lang)`** in `templates.go`, because the operator uses that number
as the Service's target port. `templates_test.go` asserts this so the two can
never silently drift.

## Programmatically creating a Job (client-go)

Read `internal/builder/job.go`. `KanikoJob` returns a `*batchv1.Job` — the same
object you'd write as YAML, but as a Go struct. Note the important fields:

- `RestartPolicy: Never` — a build runs once; don't restart it in place.
- `BackoffLimit: 2` — retry the pod at most twice (3 attempts total), matching
  the Asynq task's `MaxRetry`.
- `TTLSecondsAfterFinished: 300` — Kubernetes deletes the finished Job and its
  pod after 5 minutes, so completed builds don't pile up.
- Two volumes: the Dockerfile ConfigMap, and an `ecr-credentials` Secret mounted
  at `/kaniko/.docker/config.json` so Kaniko can authenticate to the registry.

These constructors are **pure functions** — inputs in, API objects out, no
cluster calls — which is why `job_test.go` can assert the exact args and volumes
without a running cluster.

## Watching the build and streaming logs

`builder.go` is the orchestration:

1. Mark the deployment `building`.
2. Create the ConfigMap and Job.
3. `streamBuildLogs` waits for the build pod, then **follows its stdout** and
   appends every line to the `build_logs` table — which is exactly what the API's
   SSE endpoint (doc 01) reads back to the user in real time.
4. `waitForJob` polls the Job's `status.conditions` until `Complete` or `Failed`.
5. On success, record the image URI and mark `built`; then `Deploy` creates the
   TenantApp and the operator takes over (doc 05).

## The state machine, end to end

```
queued ──(worker picks up)──▶ building ──(kaniko ok)──▶ built
   │                             │                        │
   │                             │ (kaniko fails)         │ (TenantApp created)
   ▼                             ▼                        ▼
 (enqueue fail)               failed                  deploying ──(pods ready)──▶ live
```

Every transition is a single `store.UpdateStatus` call, and each is mirrored to
the build log so the user can watch it happen.

## Related decisions

- [ADR 0006](../decisions/0006-tenantapp-ownership-and-finalizers.md) — how build
  hands off to the operator, and the namespace/finalizer model.
- [ADR 0007](../decisions/0007-kaniko-over-dind.md) — Kaniko vs Docker-in-Docker.
