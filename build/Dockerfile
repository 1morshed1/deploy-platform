# One parameterized Dockerfile builds all three platform binaries. Pass the
# target with --build-arg CMD=<api|worker|operator>:
#
#   docker build -f build/Dockerfile --build-arg CMD=api -t deploy-platform-api .
#
# Using a single Dockerfile (rather than three near-identical ones) keeps the
# build recipe in one place; the only thing that varies is which ./cmd package we
# compile. The Makefile's `docker-build` target loops over the three.
FROM golang:1.24-alpine AS builder
WORKDIR /src

# Cache dependencies first (only re-runs when go.mod/go.sum change).
COPY go.mod go.sum ./
RUN go mod download

# Build the requested command as a static binary.
COPY . .
ARG CMD=api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${CMD}

# Distroless static: no shell, no package manager, tiny attack surface. Runs as a
# non-root user (uid 65532) by default, satisfying restricted Pod Security.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/app /app
ENTRYPOINT ["/app"]
