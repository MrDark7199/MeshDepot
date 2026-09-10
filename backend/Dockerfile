# MeshDepot/PrintHub - single-container build.
# Build context = repo root (needs frontend/ and backend/).
#
#   docker build -f backend/Dockerfile -t meshdepot .

# Base images are pinned by digest so a rebuild uses the same toolchain and the
# same userland (the scrapers depend on browser behaviour; a silently newer
# Chromium can break downloads that used to work).
# Refresh a digest with:  docker manifest inspect -v <image>:<tag> | grep -m1 digest
#
# - 1. Build the frontend (Solid.js + Vite) -----------------
FROM node:20-alpine@sha256:afdf98210b07b586eb71fa22ba2e432e058e4cd1304d31ed60888755b8c865fb AS frontend
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
# npm ci rather than npm install: it honours the lockfile exactly (npm install
# may rewrite it) - reproducible builds. devDependencies are needed because
# `npm run build` starts with `tsc --noEmit`.
RUN npm ci
COPY frontend/ ./
RUN npm run build

# - 2. Build the Go binary (frontend is embedded via go:embed) --------
FROM golang:1.23-alpine@sha256:a7ecaac5efda22510d8c903bdc6b19026543f1eac3317d47363680df22161bd8 AS gobuild
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# The real Vite bundle replaces the placeholder before the build.
COPY --from=frontend /app/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /meshdepot ./cmd/app

# - 3. Runtime: Playwright/Ubuntu + Firefox (resolver) + Chromium (rod) + Tor -
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
# - Fonts ----------------------------------
# The base image ships Liberation, which fontconfig already aliases for Arial,
# Times New Roman and Courier New - those three resolve correctly and are
# metric-compatible. Everything else a Windows UA implies did NOT resolve:
# Verdana, Georgia, Tahoma, Trebuchet MS, Impact and Segoe UI all fell through
# to WenQuanYi Zen Hei, a CJK face with entirely different metrics.
#
# That matters because the resolver claims to be Chrome on Windows 10. A script
# that measures rendered text width finds those fonts present but shaped like a
# Chinese font - a mismatch between the claimed platform and the actual one, and
# exactly the kind of signal an anti-bot check scores against.
#
# carlito and caladea are metric-compatible stand-ins for Calibri and Cambria,
# dejavu covers the remaining sans requests. All from the Ubuntu archive.
# cabextract unpacks the Microsoft archives in the next step.
RUN DEBIAN_FRONTEND=noninteractive apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      fonts-liberation fonts-liberation2 \
      fonts-crosextra-carlito fonts-crosextra-caladea \
      fonts-dejavu-core \
      cabextract \
 && rm -rf /var/lib/apt/lists/*

# The genuine Arial, Verdana, Georgia, Trebuchet MS, Impact and friends. The
# archives are committed under docker/fonts/msttcorefonts rather than fetched by
# ttf-mscorefonts-installer, which downloads them from SourceForge during the
# build - that made every rebuild a bet on a third party staying reachable.
#
# They are kept in their original self-extracting form because the EULA permits
# redistribution only that way; see the README next to them. Hence cabextract
# here instead of committing the .ttf files directly.
#
# The checksums are verified before anything is unpacked, so a corrupted or
# swapped archive fails the build instead of silently changing the font set.
COPY docker/fonts/msttcorefonts /tmp/msttcorefonts
RUN cd /tmp/msttcorefonts \
 && sha256sum -c sha256sums \
 && mkdir -p /usr/share/fonts/truetype/msttcorefonts \
 && for archive in *.exe; do \
      cabextract -L -F '*.ttf' -d /usr/share/fonts/truetype/msttcorefonts "$archive" >/dev/null; \
    done \
 && fc-cache -f \
 && rm -rf /tmp/msttcorefonts

# rod uses the Chromium from the Playwright bundle rather than a second browser
# from apt: one less browser in the image, and the same version pin as Firefox.
# The revision directory changes with the Playwright version, so resolve it at
# build time and hide it behind a stable symlink.
RUN ln -s "$(ls -d /ms-playwright/chromium-*/chrome-linux/chrome | head -1)" /usr/local/bin/chromium \
 && mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix

# - 4. Firefox resolver (formerly its own sidecar container) ---------
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
ENV PLAYWRIGHT_URL=http://127.0.0.1:9001 \
    DISPLAY=:99
ENV HOME=/home/meshdepot
# No HTTP_ADDR here on purpose: the listen address belongs to the deployment,
# which derives it and the healthcheck from APP_PORT (see the environment block
# of docker-compose.yml). Without it the app logs a fatal and stops instead of
# coming up on a port nobody expects. No EXPOSE: the deployment shares the
# host's network stack, so there is no container port to publish.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["meshdepot"]
