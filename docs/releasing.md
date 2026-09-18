# Releasing playplace

This flow follows `juthrbog/awss`: GoReleaser builds the artifacts, a local Go
program generates verified package recipes, and a tag-triggered GitHub workflow
publishes them and optionally updates `juthrbog/homebrew-tap`.

**Adding or merging this configuration does not publish anything.** Only pushing
an explicit version tag starts the Release workflow. Do not push a tag until you
intend to release. Local snapshots and normal CI never publish or update the tap.

## What is automated

- `.goreleaser.yaml` builds CGO-disabled binaries for Linux, macOS, and Windows,
  on amd64 and arm64. Windows archives use ZIP; the others use tar.gz.
- Archives contain the executable, Apache-2.0 license, documentation/deployment
  material, and bash/zsh/fish completions. Web assets and generated views are
  embedded in the executable; no Node or templ runtime is required.
- Releases include a prefixed source archive and SHA-256 `checksums.txt`.
- `tools/package-release` checks the actual bytes of all seven archives against
  the manifest before generating `playplace.rb`, `PKGBUILD`, `SRCINFO`, and
  `packaging_checksums.txt`. Homebrew uses binaries; the AUR recipe builds from
  the checksummed source archive. Both support Intel and ARM64.
- Main-branch pushes and pull requests build, vet, and run race tests on Linux,
  macOS, and Windows. Linux also checks formatting and committed templ output.
  A separate Linux job builds and smoke-tests a full release snapshot.
- A `v*` tag push validates the tag, tests/vets the source, and checks generated
  views before GoReleaser creates a **draft** release. The workflow generates
  and smoke-tests package recipes, uploads them, downloads them again, and
  verifies their checksums before publishing the release.
- Stable releases update `Formula/playplace.rb` in the Homebrew tap when its
  deploy key or token is configured. Prereleases leave the stable formula alone.
  AUR submission remains manual.

The Homebrew recipe is a traditional formula, not a cask; it does not depend on
GoReleaser's deprecated `brews` integration. CI/release gates use Go's tests, vet,
and formatting checks; this setup does not copy awss's separate security-scanner
workflow or assume playplace has a golangci-lint configuration.

## One-time Homebrew setup

The target is the existing public
[juthrbog/homebrew-tap](https://github.com/juthrbog/homebrew-tap), on branch `main`.
Credentials configured for `awss` are **not** automatically available to playplace.
This change does not create credentials or modify the tap.

Before the first release:

1. Generate a **dedicated** SSH key pair for playplace publishing. Add its public
   key to the tap's **Settings → Deploy keys** with **Allow write access** enabled.
2. Store its private key as the `HOMEBREW_TAP_SSH_KEY` Actions secret in
   `juthrbog/playplace`. Never commit the key or reuse a personal SSH key.
3. Ensure branch protection permits that key to update `Formula/playplace.rb`,
   or adapt publication to create a reviewed tap PR instead of pushing directly.

Alternatively, store a fine-grained token restricted to the tap with
**Contents: read and write** as `HOMEBREW_TAP_TOKEN` in `juthrbog/playplace`.
The SSH key takes precedence when both are configured. The normal `GITHUB_TOKEN`
can publish a playplace release but cannot write to the separate tap.

With neither credential configured, the GitHub release still publishes, but the
workflow emits a warning and skips the tap update. No formula is added before a
stable release has downloadable, checksummed assets. Deploy keys do not expire
automatically; revoke or rotate them when appropriate.

After the first stable release has updated the tap:

```sh
brew install juthrbog/tap/playplace
brew test juthrbog/tap/playplace
playplace --version
```

Homebrew installation does not start a daemon or contact AWS. Formula tests only
run version, help, and completion commands. Read the deployment guide before
running account lifecycle commands or exposing `playplace serve`.

For manual recovery, verify the published recipe manifest, then copy that
release's `playplace.rb` to `Formula/playplace.rb` in the tap and commit it. Never
publish a snapshot formula: snapshot URLs intentionally point to a nonexistent
release. Do not overwrite other formulas in the tap.

## AUR recipes and manual submission

Each release generates a source-based `PKGBUILD` and `SRCINFO`. GitHub rewrites
leading-dot asset names, so users rename `SRCINFO` to `.SRCINFO` for AUR use.
The recipe requires Go 1.27.1 or newer and targets `x86_64` and `aarch64`; keep
its minimum Go version aligned with `go.mod` when upgrading the toolchain.

Download `PKGBUILD`, `SRCINFO`, `playplace.rb`, and `packaging_checksums.txt` from
the intended stable release, then:

```sh
sha256sum -c packaging_checksums.txt
cp SRCINFO .SRCINFO
makepkg --verifysource
makepkg --cleanbuild --syncdeps
makepkg --printsrcinfo > .SRCINFO
```

Run `makepkg` as a regular user on Arch Linux. Test the package and review
`.SRCINFO` before submitting or updating `playplace` in the AUR using your own
account. Check name availability first; do not overwrite another maintainer's
package. No AUR credentials or automatic AUR publication are configured, and
`yay -S playplace` is not an available install path until someone submits it.
The generated recipe can instead be used locally with `makepkg -si`.

## Local preflight without publication

Install the tools in `mise.toml` (`mise install`, with mise activated). GoReleaser
is pinned to **2.18.2**, matching both workflows. The archive smoke test also
requires bash, tar, and Ruby (`ruby -c` checks formula syntax, not installation).

```sh
task release:check       # generated views, tests, vet, GoReleaser config
task release:snapshot    # repeats checks, builds and validates local packages
```

Equivalent distribution commands, after tests and generated-view checks:

```sh
goreleaser check
goreleaser release --snapshot --clean
go run ./tools/package-release -dist dist
bash scripts/check-release.sh
```

`dist/` and `.generated/` are ignored outputs. `--clean` replaces `dist/`; do not
keep anything there that needs to survive another build. `task completions`
generates only shell completions in `.generated/completions/`.

The smoke test verifies the native archive's version, bundled files, offline
help/completion commands, source-package inputs, recipe syntax, and package
checksums. It never initializes or reconciles accounts, runs a web service,
installs packages, or publishes. Other target binaries are cross-compiled, not
executed by this script. Native Windows binary tests and an actual Arch
`makepkg` run are separate validation steps; a snapshot alone does not prove
that every installer/platform works.

**Source archives come from Git, not the working tree.** Commit intended inputs
before validating a source snapshot. A binary snapshot can include uncommitted
changes, while its source archive still reflects HEAD; the smoke test checks
for the version code, checked-in views, and license in that source archive.
For pre-commit validation, use a disposable local repository containing the
intended working tree; do not create or push a release tag just to test it.

## Publishing later

Only when you explicitly intend to release, after merging and confirming CI is
green:

```sh
git switch main
git pull --ff-only
git tag -a v0.1.0 -m 'Release v0.1.0'  # example; choose the intended version
git push origin v0.1.0
```

Use `vMAJOR.MINOR.PATCH` tags, optionally suffixed with a prerelease such as
`-rc.1`. Build metadata (`+...`) is not supported. Prerelease hyphens become dots
in Arch's `pkgver`. Never move a published tag; release a new version instead.

Watch the workflow and verify:

1. All six binary archives, the source archive, and `checksums.txt` are attached.
2. The formula, AUR recipes, and `packaging_checksums.txt` are attached and verify.
3. The release is public only after validation succeeds.
4. For stable releases, the Homebrew formula is updated and installs/tests on
   macOS and Linux. Update installation/status documentation as appropriate.

Failures after draft creation but before publication leave a draft for
inspection. A tap-update failure after publication does not remove the GitHub
release; repair the credentials or branch conflict and update the formula from
the verified published recipe. Do not blindly rerun publication or retag.

No Apple notarization, Windows Authenticode signing, or artifact-signing
credentials are configured. Checksums detect corruption but are not signatures;
do not disable OS security controls to work around unsigned binaries.
