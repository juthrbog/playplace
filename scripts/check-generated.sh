#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

# Pin templ to the same version as the runtime library in go.mod. Source
# packages build the checked-in Go output without needing a templ installation.
templ_version="$(go list -m -f '{{.Version}}' github.com/a-h/templ)"
go run "github.com/a-h/templ/cmd/templ@$templ_version" generate -path internal/web
git diff --exit-code -- internal/web
if [[ -n "$(git ls-files --others --exclude-standard -- internal/web)" ]]; then
  echo 'Untracked generated views: commit them before releasing.' >&2
  exit 1
fi
