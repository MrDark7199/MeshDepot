# MeshDepot/PrintHub - single-container build.
# Build context = repo root (needs frontend/ and backend/).
#
#   docker build -f backend/Dockerfile -t meshdepot .

# Base images are pinned by digest so a rebuild uses the same toolchain and the
# same userland (the scrapers depend on browser behaviour; a silently newer
# Chromium can break downloads that used to work).
# Refresh a digest with:  docker manifest inspect -v <image>:<tag> | grep -m1 digest
#
# ── 1. Build the frontend (Solid.js + Vite) ──────────────────────────────────
FROM node:20-alpine@sha256:afdf98210b07b586eb71fa22ba2e432e058e4cd1304d31ed60888755b8c865fb AS frontend
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
# npm ci rather than npm install: it honours the lockfile exactly (npm install
# may rewrite it) - reproducible builds. devDependencies are needed because
# `npm run build` starts with `tsc --noEmit`.
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ── 2. Build the Go binary (frontend is embedded via go:embed) ───────────────
FROM golang:1.23-alpine@sha256:a7ecaac5efda22510d8c903bdc6b19026543f1eac3317d47363680df22161bd8 AS gobuild
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# The real Vite bundle replaces the placeholder before the build.
COPY --from=frontend /app/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /meshdepot ./cmd/app

# ── 3. Runtime: Playwright/Ubuntu + Firefox (resolver) + Chromium (rod) + Tor ─
#
# The base is deliberately the Playwright image and no longer Alpine: the
# Firefox resolver (stage 4 below) needs Playwright's *patched* Firefox build,
# which exists only here - it is glibc-only and cannot be installed on
# Alpine/musl. The image already ships Node 20, every browser dependency and
# Xvfb. The v1.44.0 tag must match the "playwright" version in
# docker/playwright/package.json, otherwise npm downloads the browsers again
# instead of using the ones under /ms-playwright.
FROM mcr.microsoft.com/playwright:v1.44.0-jammy@sha256:55004c458c8daa98e219628ac622c8382fb5da6c5f99279916dcbe5c404451c0
# gosu: as root the entrypoint fixes up permissions on /data, then starts both
# the app and the resolver as an unprivileged user. The process parses foreign
# HTML in six scrapers and launches Chromium with --no-sandbox - that must not
# run as root.
# DEBIAN_FRONTEND per RUN, not as an ENV: without it tzdata opens an interactive
# region prompt and the build hangs forever.
RUN DEBIAN_FRONTEND=noninteractive apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends tor gosu ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd -g 10001 -r meshdepot \
 && useradd -u 10001 -r -g meshdepot -m -d /home/meshdepot meshdepot \
 && mkdir -p /data \
 && chown -R meshdepot:meshdepot /data /home/meshdepot
# rod uses the Chromium from the Playwright bundle rather than a second browser
# from apt: one less browser in the image, and the same version pin as Firefox.
# The revision directory changes with the Playwright version, so resolve it at
# build time and hide it behind a stable symlink.
RUN ln -s "$(ls -d /ms-playwright/chromium-*/chrome-linux/chrome | head -1)" /usr/local/bin/chromium \
 && mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix

# ── 4. Firefox resolver (formerly its own sidecar container) ─────────────────
# Node service that resolves the Cloudflare-gated MyMiniFactory download URLs
# with Firefox. Formerly a separate container; now part of this image, and the
# app no longer starts without it (health check in cmd/app/main.go).
WORKDIR /opt/resolver
# No npm ci: there is no lockfile for the resolver. The playwright version is
# pinned exactly in package.json so it matches the base image.
COPY docker/playwright/package.json ./
RUN npm install --omit=dev && npm cache clean --force
COPY docker/playwright/server.js ./

COPY --from=gobuild /meshdepot /usr/local/bin/meshdepot
COPY backend/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh
ENV CHROMIUM_BIN=/usr/local/bin/chromium \
    TOR_BIN=/usr/bin/tor \
    DB_PATH=/data/meshdepot.db \
    BASE_PATH_DATA=/data
# Single source of truth for the resolver address: the Go app reads this to find
# the resolver, and server.js parses the same value to decide what to listen on.
# Changing it here or in Compose moves both ends together. Loopback on purpose -
# no EXPOSE, no port mapping, nothing outside the container can reach it.
ENV PLAYWRIGHT_URL=http://127.0.0.1:3200 \
    DISPLAY=:99
ENV HOME=/home/meshdepot
# No HTTP_ADDR here on purpose: the listen address belongs to the deployment, which
# derives it from the same value as its port mapping and healthcheck (see the
# environment block of docker-compose.yml). Without it the app logs a fatal
# and stops instead of listening on a port nothing is mapped to. EXPOSE only
# documents the conventional port that APP_CONTAINER_PORT defaults to.
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["meshdepot"]
