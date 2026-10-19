package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
)

// provenance matches one vendored script entry in the static.go comment:
// the name, version and sha256, then the unpkg URL of the file.
var provenance = regexp.MustCompile(`// (\S+) (\d+\.\d+\.\d+), sha256 ([0-9a-f]{64}),\n// from https://unpkg\.com/([\w.-]+)@(\d+\.\d+\.\d+)/dist/([\w.-]+)\.\n`)

// TestVendoredScriptsMatchTheirProvenance keeps the static.go comment true.
// A script that changes without a new entry, or an entry with a stale hash,
// fails here. The minified files hold no line end, so a CRLF checkout
// cannot change their bytes.
func TestVendoredScriptsMatchTheirProvenance(t *testing.T) {
	b, err := os.ReadFile("static.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	entries := provenance.FindAllStringSubmatch(src, -1)
	if len(entries) < 2 {
		t.Fatalf("static.go should name htmx and idiomorph, found %d entries", len(entries))
	}
	named := map[string]bool{}
	for _, e := range entries {
		name, version, sum, pkgVersion, file := e[1], e[2], e[3], e[5], e[6]
		if version != pkgVersion {
			t.Errorf("%s: the entry says %s but the URL says %s", name, version, pkgVersion)
		}
		body, err := static.ReadFile("static/" + file)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got := sha256.Sum256(body)
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("%s: sha256 of static/%s is %x, static.go says %s", name, file, got, sum)
		}
		if file == "htmx.min.js" && !strings.Contains(string(body), `version:"`+version+`"`) {
			t.Errorf("htmx.min.js is not version %s", version)
		}
		named[file] = true
	}
	scripts, err := fs.Glob(static, "static/*.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range scripts {
		// app.js is our own code, so it has no upstream to name.
		if file := path.Base(p); file != "app.js" && !named[file] {
			t.Errorf("static/%s has no provenance entry in static.go", file)
		}
	}
	for _, file := range []string{"htmx.min.js", "idiomorph.min.js"} {
		if !named[file] {
			t.Errorf("static.go has no entry for %s", file)
		}
	}
}
