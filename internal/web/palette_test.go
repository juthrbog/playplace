package web

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The 22 color tokens and the role variables of DESIGN.md, in its order.
var (
	colorTokens = []string{"shelf", "shelf-deep", "shelf-rule", "shelf-ink", "card", "card-hover", "card-sel", "card-rule", "violet", "violet-deep", "red", "red-ink", "red-deep", "ink", "ink-2", "control-rule", "rule", "rule-soft", "ground", "paper", "paper-tint", "paper-warm"}
	colorRoles  = []string{"on-shell", "on-shell-hi", "shell-chip", "on-shell-chip", "shell-chip-hover", "search-edge"}
	// otherRoles are not light-dark() colors, so the night rule sets them again.
	otherRoles = []string{"stamp-blend", "scrim"}
)

// staticText reads an embedded text file with CRLF turned into LF, because
// a Windows checkout can carry CRLF and the repository has no .gitattributes.
func staticText(t *testing.T, name string) string {
	t.Helper()
	b, err := static.ReadFile("static/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// appCSS is app.css without comments.
func appCSS(t *testing.T) string {
	t.Helper()
	return cssComment.ReplaceAllString(staticText(t, "app.css"), "")
}

// block returns the text between the brace at open and its partner.
func block(t *testing.T, css string, open int) (body string, end int) {
	t.Helper()
	depth := 0
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[open+1 : i], i
			}
		}
	}
	t.Fatalf("unbalanced braces after offset %d", open)
	return "", 0
}

var tokensLayerStart = regexp.MustCompile(`@layer\s+tokens\s*\{`)

// tokensLayer returns the body of the one `@layer tokens { }` block and its
// span in css. The pair test reads only one block, so a second one fails.
func tokensLayer(t *testing.T, css string) (body string, start, end int) {
	t.Helper()
	m := tokensLayerStart.FindAllStringIndex(css, -1)
	if len(m) != 1 {
		t.Fatalf("app.css has %d `@layer tokens {` blocks, want exactly 1", len(m))
	}
	body, end = block(t, css, m[0][1]-1)
	return body, m[0][0], end
}

// rule returns the declarations of the rule with exactly this selector.
func rule(t *testing.T, layer, selector string) map[string]string {
	t.Helper()
	re := regexp.MustCompile(`(?:^|[\s}])` + regexp.QuoteMeta(selector) + `\s*\{`)
	m := re.FindAllStringIndex(layer, -1)
	if len(m) != 1 {
		t.Fatalf("the tokens layer has %d rules for %s, want exactly 1", len(m), selector)
	}
	body, _ := block(t, layer, m[0][1]-1)
	decls := map[string]string{}
	for _, d := range strings.Split(body, ";") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		name, value, ok := strings.Cut(d, ":")
		if !ok {
			t.Fatalf("%s: not a declaration: %q", selector, d)
		}
		name = strings.TrimSpace(name)
		if _, dup := decls[name]; dup {
			t.Fatalf("%s sets %s twice", selector, name)
		}
		decls[name] = strings.TrimSpace(value)
	}
	return decls
}

// tokenRules returns the declarations of :root and of the night rule.
func tokenRules(t *testing.T) (day, night map[string]string) {
	t.Helper()
	layer, _, _ := tokensLayer(t, appCSS(t))
	return rule(t, layer, ":root"), rule(t, layer, `:root[data-theme="night"]`)
}

// palette resolves custom properties the way a browser does under one
// color-scheme: light-dark() takes its first argument by day and its second
// at night, and var() reads the declarations of that palette.
type palette struct {
	name  string
	night bool
	vars  map[string]string
}

func palettes(t *testing.T) []palette {
	t.Helper()
	day, night := tokenRules(t)
	both := map[string]string{}
	for k, v := range day {
		both[k] = v
	}
	for k, v := range night {
		both[k] = v
	}
	return []palette{{"day", false, day}, {"night", true, both}}
}

type rgb [3]float64

var (
	hexColor = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	varRef   = regexp.MustCompile(`^var\(\s*(--[a-z0-9-]+)\s*\)$`)
)

// color resolves the token --name to an sRGB color.
func (p palette) color(name string) (rgb, error) {
	return p.eval("var(--"+name+")", map[string]bool{})
}

func (p palette) eval(expr string, seen map[string]bool) (rgb, error) {
	expr = strings.TrimSpace(expr)
	if m := varRef.FindStringSubmatch(expr); m != nil {
		if seen[m[1]] {
			return rgb{}, fmt.Errorf("%s refers to itself", m[1])
		}
		v, ok := p.vars[m[1]]
		if !ok {
			return rgb{}, fmt.Errorf("%s is not defined", m[1])
		}
		seen[m[1]] = true
		defer delete(seen, m[1])
		return p.eval(v, seen)
	}
	if inner, ok := strings.CutPrefix(expr, "light-dark("); ok && strings.HasSuffix(inner, ")") {
		args := splitArgs(strings.TrimSuffix(inner, ")"))
		if len(args) != 2 {
			return rgb{}, fmt.Errorf("light-dark() needs 2 arguments: %s", expr)
		}
		if p.night {
			return p.eval(args[1], seen)
		}
		return p.eval(args[0], seen)
	}
	m := hexColor.FindStringSubmatch(expr)
	if m == nil {
		return rgb{}, fmt.Errorf("%q is not #RGB or #RRGGBB", expr)
	}
	h := m[1]
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	var c rgb
	for i := range c {
		n, _ := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
		c[i] = float64(n)
	}
	return c, nil
}

// splitArgs splits a function argument list at its top-level commas.
func splitArgs(s string) []string {
	var out []string
	depth, from := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[from:i])
				from = i + 1
			}
		}
	}
	return append(out, s[from:])
}

// contrast is the WCAG 2 contrast ratio, not rounded.
func contrast(a, b rgb) float64 {
	lum := func(c rgb) float64 {
		var l [3]float64
		for i, v := range c {
			v /= 255
			if v <= 0.04045 {
				l[i] = v / 12.92
			} else {
				l[i] = math.Pow((v+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
	}
	hi, lo := lum(a), lum(b)
	if lo > hi {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// TestColorPairsPassInBothPalettes checks every text pair at 4.5:1 and every
// icon, border, meter and focus pair at 3:1, by day and at night. Button
// borders, rules, the disabled dashed border and the surface steps are not
// pairs: a button is known by its label, and rules carry no meaning alone.
func TestColorPairsPassInBothPalettes(t *testing.T) {
	const text, ui = 4.5, 3.0
	pairs := []struct {
		id, fg, bg string
		min        float64
	}{
		{"S1", "on-shell", "shelf", text},
		{"S2", "on-shell-hi", "shelf-deep", text},
		{"S4", "on-shell-hi", "shelf", text},
		{"S10", "on-shell-chip", "shell-chip", text},
		{"S13", "on-shell-chip", "shell-chip-hover", text},
		{"S15", "ink", "paper-warm", text},
		{"S16", "ink-2", "paper-warm", text},
		{"B1", "paper", "violet", text},
		{"B2", "paper", "violet-deep", text},
		{"B3", "ink", "card", text},
		{"B4", "ink", "card-sel", text},
		{"B5", "ink", "paper", text},
		{"B6", "ink", "paper-tint", text},
		{"B9", "paper", "red-ink", text},
		{"B10", "paper", "red-deep", text},
		{"B11", "ink-2", "rule-soft", text},
		{"B12", "ink-2", "card", text},
		{"F2", "ink-2", "paper", text},
		{"F15", "paper", "ink", text},
		{"D4", "ink", "card-hover", text},
		{"D5", "ink-2", "card-hover", text},
		{"D7", "ink-2", "card-sel", text},
		{"D8", "red-ink", "card-sel", text},
		{"T1", "ink-2", "paper-tint", text},
		{"T6", "violet", "paper", text},
		{"T7", "violet", "paper-tint", text},
		{"T8", "violet", "card", text},
		{"U2", "violet", "card-sel", text},
		{"U5", "red-ink", "card", text},
		{"U9", "violet", "card-hover", text},
		{"U10", "red-ink", "card-hover", text},
		{"W4", "shelf-ink", "paper", text},
		{"W6", "shelf-ink", "paper-tint", text},
		{"W7", "shelf-ink", "card", text},
		{"W8", "shelf-ink", "card-sel", text},
		{"W9", "shelf-ink", "card-hover", text},
		{"N9", "ink-2", "ground", text},
		{"L1", "card", "ink", text},
		{"L2", "card", "red-ink", text},
		{"P1", "red-ink", "ground", text},
		{"P2", "ink", "ground", text},
		{"P5", "violet", "ground", text},
		{"E1", "red-ink", "paper-tint", text},
		{"E2", "red-ink", "paper", text},
		{"E3", "red-ink", "paper-warm", text},

		{"S17", "ink-2", "paper-warm", ui},
		{"S18", "search-edge", "shelf", ui},
		{"S20", "on-shell", "shelf", ui},
		{"S21", "on-shell", "shelf-deep", ui},
		{"F3", "control-rule", "paper", ui},
		{"F4", "control-rule", "ground", ui},
		{"F5", "ink-2", "card", ui},
		{"F6", "ink-2", "card-sel", ui},
		{"F7", "red-ink", "paper", ui},
		{"F8", "red-ink", "card", ui},
		{"F10", "violet", "paper", ui},
		{"F12", "violet", "card", ui},
		{"F13", "violet", "card-sel", ui},
		{"I3", "shelf-ink", "card", ui},
		{"I4", "shelf-ink", "card-sel", ui},
		{"I7", "red", "paper", ui},
		{"I8", "red", "paper-tint", ui},
		{"I9", "red", "card", ui},
		{"I10", "red", "card-sel", ui},
		{"M1", "shelf-ink", "rule-soft", ui},
		{"M2", "ink", "rule-soft", ui},
		{"M3", "red", "rule-soft", ui},
		{"N7", "shelf-ink", "ground", ui},
		{"R2", "violet", "ground", ui},
		{"R4", "violet", "paper-warm", ui},
		{"R6", "violet", "card-hover", ui},
		{"P4", "red", "paper-warm", ui},
		{"P6", "red", "ground", ui},
		{"E4", "control-rule", "paper-tint", ui},
	}
	for _, p := range palettes(t) {
		for _, pr := range pairs {
			fg, err := p.color(pr.fg)
			if err != nil {
				t.Errorf("%s %s: fg --%s: %v", pr.id, p.name, pr.fg, err)
				continue
			}
			bg, err := p.color(pr.bg)
			if err != nil {
				t.Errorf("%s %s: bg --%s: %v", pr.id, p.name, pr.bg, err)
				continue
			}
			if r := contrast(fg, bg); r < pr.min {
				t.Errorf("%s %s: --%s %v on --%s %v is %.3f:1, want at least %.1f:1", pr.id, p.name, pr.fg, fg, pr.bg, bg, r, pr.min)
			}
		}
	}
}

// TestContrastReadsBothPalettes pins a few ratios from the design review, so
// a parser that reads the wrong half of light-dark() cannot pass unnoticed.
func TestContrastReadsBothPalettes(t *testing.T) {
	ps := palettes(t)
	for _, tc := range []struct {
		fg, bg     string
		day, night float64
	}{
		{"violet", "card-sel", 5.56, 4.66},
		{"search-edge", "shelf", 7.06, 3.81},
		{"control-rule", "ground", 3.05, 3.98},
		{"control-rule", "paper-tint", 3.19, 3.36},
		{"red", "card-sel", 3.63, 4.44},
		{"red-ink", "card-sel", 4.84, 5.73},
	} {
		for i, want := range []float64{tc.day, tc.night} {
			fg, err := ps[i].color(tc.fg)
			if err != nil {
				t.Fatal(err)
			}
			bg, err := ps[i].color(tc.bg)
			if err != nil {
				t.Fatal(err)
			}
			if got := contrast(fg, bg); math.Abs(got-want) > 0.006 {
				t.Errorf("%s: --%s on --%s = %.3f:1, want %.2f:1", ps[i].name, tc.fg, tc.bg, got, want)
			}
		}
	}
}

var (
	hexPair  = regexp.MustCompile(`^light-dark\(\s*(#[0-9A-Fa-f]{6})\s*,\s*(#[0-9A-Fa-f]{6})\s*\)$`)
	rolePair = regexp.MustCompile(`^light-dark\(\s*var\(--[a-z0-9-]+\)\s*,\s*var\(--[a-z0-9-]+\)\s*\)$`)
)

// TestEveryColorHasOneDayAndOneNightValue keeps one copy of each value: every
// color token is light-dark(day, night) with two literals, every color role
// is light-dark() over two tokens, and the night rule turns on the dark
// scheme and sets only the two roles that light-dark() cannot hold.
func TestEveryColorHasOneDayAndOneNightValue(t *testing.T) {
	day, night := tokenRules(t)
	if day["color-scheme"] != "light" {
		t.Errorf(":root color-scheme = %q, want light, so the night desk stays off", day["color-scheme"])
	}
	for _, name := range colorTokens {
		if v := day["--"+name]; !hexPair.MatchString(v) {
			t.Errorf(":root --%s = %q, want light-dark(#RRGGBB, #RRGGBB)", name, v)
		}
	}
	for _, name := range colorRoles {
		if v := day["--"+name]; !rolePair.MatchString(v) {
			t.Errorf(":root --%s = %q, want light-dark(var(--day), var(--night))", name, v)
		}
	}
	for _, name := range otherRoles {
		if day["--"+name] == "" {
			t.Errorf(":root has no --%s", name)
		}
	}
	var got []string
	for name := range night {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"--scrim", "--stamp-blend", "color-scheme"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the night rule sets %v, want exactly %v", got, want)
	}
	if night["color-scheme"] != "dark" {
		t.Errorf("night color-scheme = %q, want dark", night["color-scheme"])
	}
}

var (
	docColor = regexp.MustCompile(`^  ([a-z0-9-]+): "(#[0-9A-Fa-f]{6})"$`)
	docNight = regexp.MustCompile(`(?m)^\| ([a-z0-9-]+) \| (#[0-9A-Fa-f]{6}) \| (#[0-9A-Fa-f]{6}) \|`)
)

// TestTokensMatchDesignDoc keeps app.css equal to DESIGN.md: the day values
// to the frontmatter colors and the night values to the Night desk table.
func TestTokensMatchDesignDoc(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "DESIGN.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(b), "\r\n", "\n")

	front := map[string]string{}
	in := false
	for _, line := range strings.Split(doc, "\n") {
		if line == "colors:" {
			in = true
			continue
		}
		if !in {
			continue
		}
		m := docColor.FindStringSubmatch(line)
		if m == nil {
			break
		}
		front[m[1]] = m[2]
	}
	table := map[string][2]string{}
	for _, m := range docNight.FindAllStringSubmatch(doc, -1) {
		table[m[1]] = [2]string{m[2], m[3]}
	}
	// A reformatted document must not make this test check less.
	if len(front) < len(colorTokens) || len(table) < len(colorTokens) {
		t.Fatalf("DESIGN.md has %d frontmatter colors and %d night table rows, want %d of each", len(front), len(table), len(colorTokens))
	}

	day, _ := tokenRules(t)
	for _, name := range colorTokens {
		m := hexPair.FindStringSubmatch(day["--"+name])
		if m == nil {
			t.Errorf("app.css --%s = %q is not a light-dark() hex pair", name, day["--"+name])
			continue
		}
		if !strings.EqualFold(m[1], front[name]) {
			t.Errorf("--%s day = %s, DESIGN.md colors says %q", name, m[1], front[name])
		}
		row, ok := table[name]
		if !ok {
			t.Errorf("--%s has no row in the DESIGN.md night table", name)
			continue
		}
		if !strings.EqualFold(m[1], row[0]) {
			t.Errorf("--%s day = %s, the night table day column says %s", name, m[1], row[0])
		}
		if !strings.EqualFold(m[2], row[1]) {
			t.Errorf("--%s night = %s, the night table says %s", name, m[2], row[1])
		}
	}
	known := map[string]bool{}
	for _, name := range colorTokens {
		known[name] = true
	}
	for name := range front {
		if !known[name] {
			t.Errorf("DESIGN.md color %s has no token in app.css", name)
		}
	}
	for name := range table {
		if !known[name] {
			t.Errorf("DESIGN.md night row %s has no token in app.css", name)
		}
	}
}
