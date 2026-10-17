package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPlayplaceRecipes(t *testing.T) {
	dir := fixture(t, "1.2.3")
	if err := generate(dir); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, "packages", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	formula := read("playplace.rb")
	for _, want := range []string{
		"class Playplace < Formula", `license all_of: ["Apache-2.0", "OFL-1.1"]`,
		`bin.install "playplace"`, `bash_completion.install`,
		`zsh_completion.install`, `fish_completion.install`,
		`doc.install "README.md", "docs", "deploy", "licenses"`, `playplace --version`, `playplace serve --help`,
	} {
		if !strings.Contains(formula, want) {
			t.Errorf("formula missing %q", want)
		}
	}
	for _, forbidden := range []string{"awss", "config-file", "playplace init", "playplace list", "playplace reconcile", "service do"} {
		if strings.Contains(formula, forbidden) {
			t.Errorf("unsafe/copied formula content: %s", forbidden)
		}
	}
	pkgbuild := read("PKGBUILD")
	for _, want := range []string{
		"./cmd/playplace", "-X playplace/internal/cli.version=", "-mod=readonly",
		"go test ./...", `install -Dm755 playplace`, `license=('Apache-2.0' 'OFL-1.1')`,
		// The PKGBUILD builds from the source tarball, which has the OFL
		// texts only in the font folder, not in licenses/.
		"internal/web/static/fonts/OFL-PublicSans.txt internal/web/static/fonts/OFL-CourierPrime.txt",
	} {
		if !strings.Contains(pkgbuild, want) {
			t.Errorf("PKGBUILD missing %q", want)
		}
	}
	// A Windows checkout can turn the template line ends into CRLF.
	if srcinfo := strings.ReplaceAll(read("SRCINFO"), "\r\n", "\n"); !strings.Contains(srcinfo, "\tlicense = Apache-2.0\n\tlicense = OFL-1.1\n") {
		t.Errorf("SRCINFO should declare Apache-2.0 and OFL-1.1:\n%s", srcinfo)
	}
	mod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^go (\S+)\r?$`).FindStringSubmatch(string(mod))
	if len(match) != 2 {
		t.Fatal("go.mod has no Go version")
	}
	// The Arch go package has epoch 2. Without it, pacman ranks every
	// installed Go above the minimum.
	for _, name := range []string{"PKGBUILD", "SRCINFO"} {
		if !strings.Contains(read(name), "go>=2:"+match[1]) {
			t.Errorf("%s minimum Go version does not match go.mod", name)
		}
	}
}

func TestRejectNonReleaseVersions(t *testing.T) {
	for _, version := range []string{"01.2.3", "1.02.3", "1.2.03", "1.2.3+build", "1.2.3-", "1.2.3/rc"} {
		if validVersion.MatchString(version) {
			t.Errorf("accepted %q", version)
		}
	}
}
