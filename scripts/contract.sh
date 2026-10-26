#!/usr/bin/env bash
# academy/contract is a pinned copy of OpsForge's contract/ (its API's schema, OpenAPI document and golden documents).
#   scripts/contract.sh update <repo> <ref>   fetch contract/ from OpsForge's git repository at <ref> (a tag once
#                                             OpsForge releases; any git URL or clone) and record it in PINNED
#   scripts/contract.sh check                 the pinned copy is what PINNED recorded (offline: no OpsForge needed)
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pinned="$root/academy/contract"
files=(toolchain.schema.json openapi.yaml README.md golden)

digest() {
	(cd "$pinned" && find "${files[@]}" -type f | LC_ALL=C sort | xargs sha256sum | sha256sum | cut -d' ' -f1)
}

case "${1:-}" in
update)
	repo="${2:?repo}" ref="${3:?ref}"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	git -C "$tmp" init -q
	git -C "$tmp" fetch -q --depth 1 "$repo" "$ref"
	git -C "$tmp" checkout -q FETCH_HEAD
	for f in "${files[@]}"; do
		rm -rf "${pinned:?}/$f"
		cp -r "$tmp/contract/$f" "$pinned/"
	done
	version="$(sed -n 's/^  version: "\(.*\)"$/\1/p' "$tmp/contract/openapi.yaml" | head -1)"
	printf 'opsforge %s (%s), contract %s\nsha256 %s\n' "$(git -C "$tmp" rev-parse --short=12 HEAD)" "$ref" \
		"$version" "$(digest)" >"$pinned/PINNED"
	cat "$pinned/PINNED"
	;;
check)
	want="$(sed -n 's/^sha256 //p' "$pinned/PINNED")"
	if [ "$(digest)" != "$want" ]; then
		echo "academy/contract differs from what PINNED recorded: edit OpsForge's contract, then make contract-update" >&2
		exit 1
	fi
	;;
*)
	echo "usage: $0 update <repo> <ref> | check" >&2
	exit 2
	;;
esac
