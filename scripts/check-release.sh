#!/usr/bin/env bash
set -euo pipefail

# Validate a local snapshot without installing it or contacting AWS. Do not
# replace these offline commands with init/list/reconcile or start the service.
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
dist="${1:-dist}"
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo 'Archive smoke test requires macOS or Linux.' >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo 'Unsupported smoke-test architecture.' >&2; exit 1 ;;
esac
archives=("$dist"/playplace_*_"${os}_${arch}".tar.gz)
if [[ ${#archives[@]} -ne 1 || ! -f "${archives[0]}" ]]; then
  echo 'Expected exactly one native release archive.' >&2
  exit 1
fi
archive="${archives[0]}"
version="${archive##*/playplace_}"
version="${version%_"${os}"_"${arch}".tar.gz}"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

# Packaging must work without AWS credentials, usable endpoints, or history.
export AWS_EC2_METADATA_DISABLED=true
export AWS_CONFIG_FILE="$tmpdir/missing-config"
export AWS_SHARED_CREDENTIALS_FILE="$tmpdir/missing-credentials"
export PLAYPLACE_AWS_ENDPOINT=http://127.0.0.1:1
export PLAYPLACE_HISTORY_FILE="$tmpdir/must-not-exist.jsonl"
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_PROFILE

tar -xzf "$archive" -C "$tmpdir"
[[ "$("$tmpdir/playplace" --version)" == "playplace version $version" ]]
for file in LICENSE README.md DESIGN.md DEVELOPMENT.md \
  deploy/README.md deploy/aws/playground-safeguards-scp.json \
  completions/playplace.bash completions/_playplace completions/playplace.fish; do
  test -s "$tmpdir/$file"
done
"$tmpdir/playplace" --help > "$tmpdir/help"
grep -q reconcile "$tmpdir/help"
"$tmpdir/playplace" serve --help > "$tmpdir/serve-help"
grep -q oidc "$tmpdir/serve-help"
for shell in bash zsh fish; do
  "$tmpdir/playplace" completion "$shell" > "$tmpdir/completion-$shell"
  test -s "$tmpdir/completion-$shell"
done
test ! -e "$PLAYPLACE_HISTORY_FILE"

# The source package must contain the checked-in views and license. GoReleaser
# archives Git, not uncommitted source changes; run this from the intended commit.
mkdir "$tmpdir/source"
tar -xzf "$dist/playplace_${version}_source.tar.gz" -C "$tmpdir/source"
for file in LICENSE go.mod cmd/playplace/main.go internal/cli/version.go \
  internal/web/views_templ.go; do
  test -s "$tmpdir/source/playplace-$version/$file"
done
ruby -c "$dist/packages/playplace.rb"
bash -n "$dist/packages/PKGBUILD"
if command -v sha256sum >/dev/null; then
  (cd "$dist/packages" && sha256sum -c packaging_checksums.txt)
else
  (cd "$dist/packages" && shasum -a 256 -c packaging_checksums.txt)
fi
echo "Release archive and package recipes passed smoke checks ($os/$arch)."
