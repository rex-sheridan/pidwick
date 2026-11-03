#!/usr/bin/env bash
# Build Pidwick from this checkout and install it.
#
#   ./install.sh              install to $GOBIN, else $(go env GOPATH)/bin
#   ./install.sh ~/.local/bin install to a directory of your choice
set -euo pipefail

cd "$(dirname "$0")"

command -v go >/dev/null || { echo "install.sh: Go is not installed (https://go.dev/dl/)" >&2; exit 1; }

dest="${1:-${GOBIN:-$(go env GOBIN)}}"
dest="${dest:-$(go env GOPATH)/bin}"
mkdir -p "$dest"

# Build to a temp file, then move into place, so a failed build never
# leaves a half-written binary and a running copy isn't overwritten in place.
tmp="$(mktemp -t pidwick.XXXXXX)"
trap 'rm -f "$tmp"' EXIT

echo "Building…"
go build -o "$tmp" .
install -m 755 "$tmp" "$dest/pidwick"
echo "Installed $dest/pidwick"

# Warn if a different pidwick would still win on PATH.
if found="$(command -v pidwick)" && [ "$found" != "$dest/pidwick" ]; then
  echo "Note: 'pidwick' on your PATH resolves to $found, not the one just installed." >&2
elif [ -z "${found:-}" ]; then
  echo "Note: $dest is not on your PATH." >&2
fi
