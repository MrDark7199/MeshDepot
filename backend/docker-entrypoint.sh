#!/bin/sh
# Brings up the container contents in this order:
#   1. fix permissions on /data (as root), then drop privileges
#   2. Xvfb  - virtual X server for the resolver's headful Firefox
#   3. node  - Firefox resolver on $PLAYWRIGHT_URL (formerly its own container)
#   4. the app itself
#
# The detour via root is necessary because /data is a volume: when an existing
# installation is upgraded, the files there still belong to root, and a
# container starting directly as meshdepot could no longer open the database.
# So: chown once, then hand over privileges via gosu - the actual process
# (including Chromium, Firefox and Tor) never runs as root.
#
# Overriding the container with `user:` in Compose skips the chown branch: we
# are already unprivileged then and start straight through.
set -e

if [ "$(id -u)" = "0" ]; then
	mkdir -p /data
	chown -R meshdepot:meshdepot /data 2>/dev/null || true
	# This script again, just without root. That keeps exactly one path through
	# startup instead of two that can drift apart.
	exec gosu meshdepot:meshdepot "$0" "$@"
fi

# ── Resolver token ───────────────────────────────────────────────────────────
# The resolver refuses to start without one. It used to be the operator's job
# because app and sidecar were separate containers; both now share this process
# environment, so generate a secret that never leaves the container.
if [ -z "$PLAYWRIGHT_TOKEN" ]; then
	PLAYWRIGHT_TOKEN=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
fi
export PLAYWRIGHT_TOKEN

# ── Xvfb ─────────────────────────────────────────────────────────────────────
# On some sites Cloudflare only lets a *headful* Firefox through, and that needs
# a display. After a container restart a stale lock is left behind; Xvfb then
# refuses to start and every headful call fails silently with "cannot open
# display :99". So clear the lock before each start, and respawn if it dies.
clear_x_locks() {
	rm -f /tmp/.X99-lock /tmp/.X11-unix/X99 2>/dev/null || true
}

clear_x_locks
while :; do
	Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp || true
	clear_x_locks
	sleep 1
done &

# ── Firefox resolver ─────────────────────────────────────────────────────────
# Respawned as well: the app only checks the resolver at startup, so without
# this a dead node would break MyMiniFactory downloads silently.
while :; do
	node /opt/resolver/server.js || echo "[entrypoint] resolver exited - restarting in 2s"
	sleep 2
done &

exec "$@"
