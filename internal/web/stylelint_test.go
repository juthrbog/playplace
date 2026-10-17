package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	hexLiteral = regexp.MustCompile(`(?i)#(?:[0-9a-f]{8}|[0-9a-f]{6}|[0-9a-f]{3,4})\b`)
	colorFunc  = regexp.MustCompile(`(?i)\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|color-mix|light-dark)\(`)
	// namedColor matches a CSS named color or system color as a whole word.
	// Hyphens count as part of a word, so var(--red) and .status-red do not.
	namedColor = regexp.MustCompile(`(?i)(?:^|[^\w-])(` + strings.Join(cssColorNames, "|") + `)(?:[^\w-]|$)`)
	cssURL     = regexp.MustCompile(`url\([^)]*\)|"[^"]*"|'[^']*'`)
	cssVarCall = regexp.MustCompile(`var\(\s*--[\w-]+\s*\)`)
)

// cssColorNames are the CSS named colors and system colors.
var cssColorNames = []string{
	"aliceblue", "antiquewhite", "aqua", "aquamarine", "azure", "beige", "bisque", "black", "blanchedalmond", "blue", "blueviolet", "brown", "burlywood", "cadetblue", "chartreuse", "chocolate", "coral", "cornflowerblue", "cornsilk", "crimson", "cyan", "darkblue", "darkcyan", "darkgoldenrod", "darkgray", "darkgreen", "darkgrey", "darkkhaki", "darkmagenta", "darkolivegreen", "darkorange", "darkorchid", "darkred", "darksalmon", "darkseagreen", "darkslateblue", "darkslategray", "darkslategrey", "darkturquoise", "darkviolet", "deeppink", "deepskyblue", "dimgray", "dimgrey", "dodgerblue", "firebrick", "floralwhite", "forestgreen", "fuchsia", "gainsboro", "ghostwhite", "gold", "goldenrod", "gray", "green", "greenyellow", "grey", "honeydew", "hotpink", "indianred", "indigo", "ivory", "khaki", "lavender", "lavenderblush", "lawngreen", "lemonchiffon", "lightblue", "lightcoral", "lightcyan", "lightgoldenrodyellow", "lightgray", "lightgreen", "lightgrey", "lightpink", "lightsalmon", "lightseagreen", "lightskyblue", "lightslategray", "lightslategrey", "lightsteelblue", "lightyellow", "lime", "limegreen", "linen", "magenta", "maroon", "mediumaquamarine", "mediumblue", "mediumorchid", "mediumpurple", "mediumseagreen", "mediumslateblue", "mediumspringgreen", "mediumturquoise", "mediumvioletred", "midnightblue", "mintcream", "mistyrose", "moccasin", "navajowhite", "navy", "oldlace", "olive", "olivedrab", "orange", "orangered", "orchid", "palegoldenrod", "palegreen", "paleturquoise", "palevioletred", "papayawhip", "peachpuff", "peru", "pink", "plum", "powderblue", "purple", "rebeccapurple", "red", "rosybrown", "royalblue", "saddlebrown", "salmon", "sandybrown", "seagreen", "seashell", "sienna", "silver", "skyblue", "slateblue", "slategray", "slategrey", "snow", "springgreen", "steelblue", "tan", "teal", "thistle", "tomato", "turquoise", "violet", "wheat", "white", "whitesmoke", "yellow", "yellowgreen",
	"accentcolor", "accentcolortext", "activetext", "buttonborder", "buttonface", "buttontext", "canvas", "canvastext", "field", "fieldtext", "graytext", "highlight", "highlighttext", "linktext", "mark", "marktext", "selecteditem", "selecteditemtext", "visitedtext",
}

type cssDecl struct{ prop, value string }

// declarations returns every declaration in css. It reads each piece of text
// between two of the marks `{`, `}` and `;`. A piece that ends at `{` is a
// selector or an at-rule prelude. Any other piece with a `:` is a
// declaration. So a rule that holds a nested rule (CSS nesting) keeps its own
// declarations in view.
func declarations(css string) []cssDecl {
	var out []cssDecl
	from := 0
	for i := 0; i < len(css); i++ {
		switch css[i] {
		case '{':
			from = i + 1
		case '}', ';':
			if prop, value, ok := strings.Cut(css[from:i], ":"); ok {
				out = append(out, cssDecl{strings.ToLower(strings.TrimSpace(prop)), strings.TrimSpace(value)})
			}
			from = i + 1
		}
	}
	return out
}

// TestDeclarationsReadNestedRules keeps the lint able to read CSS nesting,
// which every browser of the 2024 floor supports.
func TestDeclarationsReadNestedRules(t *testing.T) {
	got := declarations(`@media (max-width: 640px) { .x { color: #fff; font: 12px serif; &:hover { color: var(--ink) } --ink: red } }`)
	want := []cssDecl{{"color", "#fff"}, {"font", "12px serif"}, {"color", "var(--ink)"}, {"--ink", "red"}}
	if len(got) != len(want) {
		t.Fatalf("declarations = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("declaration %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestColorsOnlyInTokenLayer keeps every color value in the tokens layer of
// app.css, so the palette test sees every color the page can paint. Outside
// the layer a rule reads var(--…), currentColor, transparent or inherit. It
// never sets a token again, because that changes the palette without a
// literal.
func TestColorsOnlyInTokenLayer(t *testing.T) {
	css := appCSS(t)
	layer, start, end := tokensLayer(t, css)

	// Inside the layer a color literal is only the value of a --name.
	for _, d := range declarations(layer) {
		if strings.HasPrefix(d.prop, "--") {
			continue
		}
		if hexLiteral.MatchString(d.value) || colorFunc.MatchString(d.value) || namedColor.MatchString(d.value) {
			t.Errorf("tokens layer: %s: %s sets a color outside a custom property", d.prop, d.value)
		}
	}

	outside := css[:start] + css[end+1:]
	tokens := map[string]bool{}
	for _, names := range [][]string{colorTokens, colorRoles, otherRoles} {
		for _, n := range names {
			tokens["--"+n] = true
		}
	}
	for _, d := range declarations(outside) {
		if tokens[d.prop] {
			t.Errorf("%s is set outside the tokens layer: %s: %s", d.prop, d.prop, d.value)
		}
		// The font shorthand resets font-variant-numeric and loses the
		// tabular figures.
		if d.prop == "font" && d.value != "inherit" {
			t.Errorf("font shorthand outside `font: inherit`: font: %s", d.value)
		}
		v := cssVarCall.ReplaceAllString(cssURL.ReplaceAllString(d.value, ""), "")
		if hexLiteral.MatchString(v) || colorFunc.MatchString(v) {
			t.Errorf("color literal outside the tokens layer: %s: %s", d.prop, d.value)
		}
		if m := namedColor.FindStringSubmatch(v); m != nil {
			t.Errorf("named color %q outside the tokens layer: %s: %s", m[1], d.prop, d.value)
		}
	}
	if strings.Contains(css, "-apple-system") {
		t.Error("app.css names -apple-system, the face is Public Sans with system-ui")
	}

	js := staticText(t, "app.js")
	if m := hexLiteral.FindString(js); m != "" {
		t.Errorf("app.js has the color literal %s", m)
	}
	if m := colorFunc.FindString(js); m != "" {
		t.Errorf("app.js has the color function %s", m)
	}
}

var (
	templColorAttr = regexp.MustCompile(`(?:^|\s)(fill|stroke|stop-color|flood-color|lighting-color|color)\??\s*=\s*("[^"]*"|'[^']*'|\{)`)
	svgElement     = regexp.MustCompile(`(?s)<svg\b.*?</svg>`)
	templSpread    = regexp.MustCompile(`\{\s*[\w.]+\s*\.\.\.\s*\}`)
	templStyleAttr = regexp.MustCompile(`(?i)(?:^|\s)style\??\s*=`)
	templStyleTag  = regexp.MustCompile(`(?i)<style\b`)
	// templCSS is a templ css component. It renders a <style> element.
	templCSS = regexp.MustCompile(`^\s*css\s+\w+\(`)
	// templStyleKey is a "style" key in an attribute map such as
	// templ.Attributes. It renders a style attribute. The key can be in
	// double quotes, single quotes or backticks.
	templStyleKey = regexp.MustCompile("(?i)[\"'`]style[\"'`]\\s*:")
	// templAnySpread is a templ spread, { x... }. An attribute spread can
	// carry a style attribute that a .go helper builds, out of view of this
	// lint. templChildren is the one spread that is not an attribute.
	templAnySpread = regexp.MustCompile(`\.\.\.\s*\}`)
	templChildren  = regexp.MustCompile(`\{\s*children\s*\.\.\.\s*\}`)
)

// templates returns the name and source of every template in the package,
// with CRLF line ends turned into LF.
func templates(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.templ")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates found: %v", err)
	}
	out := map[string]string{}
	for _, name := range files {
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = strings.ReplaceAll(string(b), "\r\n", "\n")
	}
	return out
}

// inlineStyleProblems names each way that one template line can put a style
// or a color into the markup.
func inlineStyleProblems(line string) []string {
	var out []string
	if templStyleAttr.MatchString(line) {
		out = append(out, "style attribute")
	}
	if templStyleTag.MatchString(line) {
		out = append(out, "<style> element")
	}
	if templCSS.MatchString(line) {
		out = append(out, "templ css component")
	}
	if templStyleKey.MatchString(line) {
		out = append(out, "style key in an attribute map")
	}
	if templAnySpread.MatchString(templChildren.ReplaceAllString(line, "")) {
		out = append(out, "attribute spread")
	}
	if m := hexLiteral.FindString(line); m != "" {
		out = append(out, "hex color "+m)
	}
	return out
}

// TestTemplHasNoInlineStyle keeps every style in app.css. The CSP has no
// 'unsafe-inline' for styles, so the browser drops a style attribute or a
// <style> element. A templ css component, a "style" key in an attribute map
// and an attribute spread can make the same markup, so they fail too. A hex
// color in markup is a color that the palette test never sees. Ids such as
// #accounts and #i-mark do not match, because a word character follows the
// hex letters.
func TestTemplHasNoInlineStyle(t *testing.T) {
	for name, src := range templates(t) {
		for _, line := range strings.Split(src, "\n") {
			for _, problem := range inlineStyleProblems(line) {
				t.Errorf("%s: %s: %s", name, problem, strings.TrimSpace(line))
			}
		}
	}
}

// TestInlineStyleLintCatchesEachForm keeps the lint able to see each way a
// template can add a style. It also keeps the lint quiet on markup that the
// page uses.
func TestInlineStyleLintCatchesEachForm(t *testing.T) {
	for _, line := range []string{
		`<div style="color: red"></div>`,
		`<div style?={ on }></div>`,
		`<style>p { color: red }</style>`,
		`css bad() {`,
		`{{ attrs := templ.Attributes{"style": "color: red"} }}`,
		`{{ attrs := templ.Attributes{'style': "color: red"} }}`,
		"{{ attrs := templ.Attributes{`style`: \"color: red\"} }}",
		`<div { attrs... }></div>`,
		`<div { styleAttrs()... }></div>`,
		`<div { templ.Attributes{"x": "y"}... }></div>`,
		`<p class="x">#ff0000</p>`,
	} {
		if len(inlineStyleProblems(line)) == 0 {
			t.Errorf("the lint misses %s", line)
		}
	}
	for _, line := range []string{
		`{ children... }`,
		`<section id="accounts" class="panel">`,
		`<use href="#i-mark"></use>`,
		`<span class="status">{ "Loading..." }</span>`,
	} {
		if got := inlineStyleProblems(line); len(got) > 0 {
			t.Errorf("the lint flags %s: %v", line, got)
		}
	}
}

// TestStylesheetHasNoInterimLayer keeps the old page style from coming back.
// Each panel has its own component rules now, so the interim layer that held
// the old class rules is gone, and the layer order names only the five
// layers of the design.
func TestStylesheetHasNoInterimLayer(t *testing.T) {
	css := appCSS(t)
	if strings.Contains(css, "@layer interim") {
		t.Error("app.css still has an @layer interim block")
	}
	if strings.Contains(css, "interim") {
		t.Error("app.css still names the interim layer")
	}
	if !strings.Contains(css, "@layer tokens, base, layout, components, utilities;") {
		t.Error("app.css must declare the layers tokens, base, layout, components, utilities in that order")
	}
}

// TestNoColorAttributesInTempl keeps colors out of the markup: an SVG or
// element color attribute in a template is only none or currentColor, so
// the stylesheet decides every color. A dynamic value or an attribute spread
// inside an <svg> can carry any color, so it fails too.
func TestNoColorAttributesInTempl(t *testing.T) {
	for name, src := range templates(t) {
		for _, m := range templColorAttr.FindAllStringSubmatch(src, -1) {
			if v := m[2]; v != `"none"` && v != `'none'` && v != `"currentColor"` && v != `'currentColor'` {
				t.Errorf("%s: %s=%s, markup allows only none or currentColor", name, m[1], v)
			}
		}
		for _, svg := range svgElement.FindAllString(src, -1) {
			if m := templSpread.FindString(svg); m != "" {
				t.Errorf("%s: attribute spread %s inside an <svg>", name, m)
			}
		}
	}
}
