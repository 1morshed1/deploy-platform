# Multi-stage build for a Node.js application.
#
# Stage 1 installs ALL dependencies (including dev deps needed to build) and runs
# the build. Stage 2 keeps only what's needed at runtime.
FROM node:20-alpine AS builder
WORKDIR /app

# Copy manifests first for layer caching (see the Go Dockerfile for the rationale).
COPY package*.json ./
# npm ci is the reproducible, lockfile-driven install (unlike npm install).
# --production=false ensures devDependencies (build tools) are present.
RUN npm ci --production=false

COPY . .
RUN npm run build

# Stage 2: runtime.
FROM node:20-alpine
WORKDIR /app
# Copy the resolved node_modules and built output from the builder stage.
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/dist ./dist
COPY --from=builder /app/package.json .
EXPOSE 3000
CMD ["node", "dist/index.js"]
