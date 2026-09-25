# Multi-stage build for a Go application.
#
# Stage 1 ("builder") compiles a static binary. Stage 2 copies ONLY that binary
# into a tiny base image. The user's source code, the Go toolchain, and all build
# caches stay in stage 1 and never ship — the final image is a few MB.
FROM golang:1.22-alpine AS builder
WORKDIR /app

# Copy just the module files first and download deps. This layer is cached and
# only re-runs when go.mod/go.sum change, so most rebuilds skip dependency
# downloads entirely.
COPY go.* ./
RUN go mod download

# Now copy the rest of the source and build.
COPY . .
# CGO_ENABLED=0 makes a fully static binary with no libc dependency, so it runs
# on the minimal alpine base below (or even scratch).
RUN CGO_ENABLED=0 go build -o /server .

# Stage 2: the runtime image.
FROM alpine:3.19
# ca-certificates so the app can make outbound HTTPS calls.
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /server
EXPOSE 8080
CMD ["/server"]
