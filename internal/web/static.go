package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
)

// Static holds the vendored htmx build and the page script. Serving them
// from the binary means the UI trusts no third-party host and can run under
// a strict Content-Security-Policy.
//
// htmx 2.0.10, sha256 71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de,
// from https://unpkg.com/htmx.org@2.0.10/dist/htmx.min.js.
//
//go:embed static
var static embed.FS

func staticHandler() http.Handler {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}

// csp is the policy every page is served with. Styles stay inline in the
// layout, so style-src allows that; scripts and requests come only from this
// origin and nothing may frame the UI.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'self'"

// assetVersion is a short hash of the embedded static files, so icon and
// logo URLs change whenever the files do and browsers drop cached copies.
var assetVersion = func() string {
	h := sha256.New()
	fs.WalkDir(static, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := static.ReadFile(path)
		h.Write([]byte(path))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:8]
}()

// asset returns a versioned URL for a file under static. favicon.ico is
// also served at the root for browsers that never read the link tags.
func asset(name string) string {
	return "/static/" + name + "?v=" + assetVersion
}
