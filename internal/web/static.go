package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Static holds the vendored htmx build, the page script, the stylesheet and
// the fonts. Serving them from the binary means the UI trusts no third-party
// host and can run under a strict Content-Security-Policy.
//
// htmx 2.0.10, sha256 71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de,
// from https://unpkg.com/htmx.org@2.0.10/dist/htmx.min.js.
//
// Public Sans (variable, wght) and Courier Prime 400 and 700, latin and
// latin-ext woff2 subsets, come from Fontsource 5.3.0
// (@fontsource-variable/public-sans and @fontsource/courier-prime on npm).
// They are under the SIL Open Font License 1.1. The license texts sit next
// to the files in static/fonts. The sha256 of each file:
//
//	5ed4d31c988e73b258894244f209069ebe77dc7e564861954b21198b6de90d68  public-sans-latin-wght-normal.woff2
//	3a00a32f0242b723dcea79935747d6d27dd93675d03ef23f470dfe274e79586a  public-sans-latin-ext-wght-normal.woff2
//	f09844e1f4fe9e67df2cc77474519e68756d559b4648278b9dd4f03c8ac9c31f  courier-prime-latin-400-normal.woff2
//	efbea074e3d87eecd5bcc05fc363853b68e703a45ecb896cd804c80c9918acd0  courier-prime-latin-ext-400-normal.woff2
//	5c7ae1cc9aae183453565c836b74a1b988f5f2e52b65098decb909b3930957ec  courier-prime-latin-700-normal.woff2
//	b7ce6854b9082533919ab47566fc92e9dac95d9a9cc209f5ee1922faa6a19ea9  courier-prime-latin-ext-700-normal.woff2
//	705a497c781f6463ff819abb513bb495f3fb05256f1d20321e3275813c0bafa5  OFL-PublicSans.txt
//	195bfcfb42f32343efc8475e902c3c4936cda7a9c435c39634e03c1e85fa862b  OFL-CourierPrime.txt
//
//go:embed static
var static embed.FS

// staticHandler serves the embedded files with cache headers. A URL that
// carries the current ?v= never changes, so browsers keep it for a year.
// Any other URL, such as a font that app.css loads by a relative URL,
// revalidates against the asset version and gets a 304 when nothing changed.
func staticHandler() http.Handler {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if info, err := fs.Stat(sub, name); err == nil && !info.IsDir() {
			h := w.Header()
			if r.URL.Query().Get("v") == assetVersion {
				h.Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				// http.ServeContent compares a preset ETag with If-None-Match.
				h.Set("Cache-Control", "no-cache")
				h.Set("ETag", `"`+assetVersion+`"`)
			}
			// Go's built-in type table has no .woff2, and the OS table
			// differs between machines.
			if strings.HasSuffix(name, ".woff2") {
				h.Set("Content-Type", "font/woff2")
			}
		}
		files.ServeHTTP(w, r)
	}))
}

// csp is the policy every page is served with. Scripts, styles, fonts and
// requests come only from this origin. The policy allows no inline style or
// script, so every style lives in static/app.css. No other site can frame
// the UI.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'self'"

// assetVersion is a short hash of the embedded static files, so icon,
// script and stylesheet URLs change whenever the files do and browsers drop
// cached copies.
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
