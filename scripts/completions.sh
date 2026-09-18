#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
mkdir -p .generated/completions
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

# Generate on the build host; never try to execute a cross-compiled binary.
go build -o "$tmpdir/playplace" ./cmd/playplace
"$tmpdir/playplace" completion bash > .generated/completions/playplace.bash
"$tmpdir/playplace" completion zsh > .generated/completions/_playplace
"$tmpdir/playplace" completion fish > .generated/completions/playplace.fish
