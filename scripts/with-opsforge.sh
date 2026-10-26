#!/usr/bin/env bash
# Runs a command with an OpsForge API: the one named by ACADEMY_OPSFORGE_URL and ACADEMY_OPSFORGE_TOKEN_FILE (and
# ACADEMY_OPSFORGE_CA_FILE for a self-signed https one) when set, on this machine or another; else a throwaway fake
# (academy/cmd/opsforge-fake: an empty lab, every step valid), with a warning. `make academy-validate` compiles the
# packs this way: only a real OpsForge checks their steps. Usage: scripts/with-opsforge.sh <command> [args...]
set -euo pipefail

if [ -n "${ACADEMY_OPSFORGE_URL:-}" ]; then
	exec "$@"
fi
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
echo "with-opsforge: no ACADEMY_OPSFORGE_URL: using the fake OpsForge (steps are not checked)" >&2
tmp="$(mktemp -d)"
(cd "$root/academy" && CGO_ENABLED=0 go build -o "$tmp/opsforge-fake" ./cmd/opsforge-fake)
"$tmp/opsforge-fake" -addr 127.0.0.1:0 -token-file "$tmp/token" -url-file "$tmp/url" 2>"$tmp/fake.log" &
fake=$!
cleanup() {
	kill "$fake" 2>/dev/null || true
	wait "$fake" 2>/dev/null || true
	rm -rf "$tmp"
}
trap cleanup EXIT

for _ in $(seq 50); do
	[ -s "$tmp/url" ] && break
	sleep 0.1
done
ACADEMY_OPSFORGE_URL="$(cat "$tmp/url")"
export ACADEMY_OPSFORGE_URL ACADEMY_OPSFORGE_TOKEN_FILE="$tmp/token"
"$@"
