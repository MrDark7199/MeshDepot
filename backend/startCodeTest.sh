#!/bin/sh
# Run the backend test suite without a local Go toolchain, using the golang image
# from the Docker daemon. Module and build caches live in a named volume so
# repeated runs are fast.
#
#   ./startCodeTest.sh                     # every test in every package, with coverage
#   ./startCodeTest.sh check               # go build ./... && go vet ./...
#   ./startCodeTest.sh test ./internal/api/        # one package
#   ./startCodeTest.sh test -run TestFoo ./internal/api/   # extra args are forwarded
#   ./startCodeTest.sh fmt ./...           # anything else goes to `go` unchanged
#
# The full run takes a few minutes; internal/api, pwmx, auth and scheduler carry
# the long tests.
#
# Runs from the host Docker daemon (flatpak-spawn when inside the PhpStorm
# sandbox, plain docker otherwise).
set -eu

image=golang:1.23-alpine
volume=meshdepot-gocache
src=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# The repository root is mounted, not just backend/: the OpenAPI drift test
# reads ../../../docs/openapi.yaml.
root=$(CDPATH= cd -- "$src/.." && pwd)
rel=${src#"$root"/}

if command -v flatpak-spawn >/dev/null 2>&1 && [ -n "${FLATPAK_ID:-}" ]; then
	docker() { flatpak-spawn --host docker "$@"; }
fi

docker volume create "$volume" >/dev/null

run() {
	docker run --rm \
		-v "$root:/repo" -v "$volume:/gocache" -w "/repo/$rel" \
		-e GOMODCACHE=/gocache/mod -e GOCACHE=/gocache/build \
		"$image" "$@"
}

# The full suite is quiet for minutes at a time: `go test` only prints a package
# once it is finished, and the long ones come last. Without a sign of life the
# run looks stuck right after the first zero-coverage lines, so announce the
# duration up front and tick every half minute while the slow packages work.
heartbeat() {
	seconds=0
	while sleep 30; do
		seconds=$((seconds + 30))
		echo "  ... still running (${seconds}s) - waiting for the slow packages" >&2
	done
}

full_suite() {
	echo "Running every test in every package. Expect about five minutes:" >&2
	echo "packages without tests report immediately, then internal/api (~3 min)," >&2
	echo "pwmx, scheduler and auth stay silent until each one is finished." >&2

	heartbeat &
	heartbeat_pid=$!
	trap 'kill "$heartbeat_pid" 2>/dev/null || true' EXIT INT TERM

	run go test -cover ./...

	kill "$heartbeat_pid" 2>/dev/null || true
	trap - EXIT INT TERM
	echo "Suite finished." >&2
}

case "${1:-all}" in
all)
	full_suite
	;;
check)
	run go build ./...
	run go vet ./...
	;;
test)
	shift
	if [ "$#" -gt 0 ]; then
		run go test "$@"
	else
		full_suite
	fi
	;;
*)
	run go "$@"
	;;
esac
