package web

import (
	"regexp"
	"strings"
	"testing"
)

// mediaStart is the head of an @media block. The group is its query.
var mediaStart = regexp.MustCompile(`@media\s+([^{]+?)\s*\{`)

// layerRules returns the declarations of the innermost rules of one
// layer of app.css, and of each media block of that layer by its query.
// A key is the selector list as cssRules writes it.
func layerRules(t *testing.T, layer string) (top map[string]map[string]string, media map[string]map[string]map[string]string) {
	t.Helper()
	css := appCSS(t)
	m := regexp.MustCompile(`@layer\s+`+regexp.QuoteMeta(layer)+`\s*\{`).FindAllStringIndex(css, -1)
	if len(m) != 1 {
		t.Fatalf("app.css has %d `@layer %s {` blocks, want exactly 1", len(m), layer)
	}
	body, _ := block(t, css, m[0][1]-1)
	media = map[string]map[string]map[string]string{}
	var rest strings.Builder
	from := 0
	for _, mm := range mediaStart.FindAllStringSubmatchIndex(body, -1) {
		// A media block inside another one is part of the outer body.
		if mm[0] < from {
			continue
		}
		inner, end := block(t, body, mm[1]-1)
		query := strings.Join(strings.Fields(body[mm[2]:mm[3]]), " ")
		if media[query] == nil {
			media[query] = map[string]map[string]string{}
		}
		mergeRules(media[query], inner)
		rest.WriteString(body[from:mm[0]])
		from = end + 1
	}
	rest.WriteString(body[from:])
	top = map[string]map[string]string{}
	mergeRules(top, rest.String())
	return top, media
}

// mergeRules adds the innermost rules of css to into by their selector
// lists. A later value of a property wins, as in the browser.
func mergeRules(into map[string]map[string]string, css string) {
	for _, m := range cssRule.FindAllStringSubmatch(css, -1) {
		key := strings.Join(splitSelectors(m[1]), ", ")
		if into[key] == nil {
			into[key] = map[string]string{}
		}
		for _, d := range declarations("{" + m[2] + "}") {
			into[key][d.prop] = d.value
		}
	}
}

// wantDecls fails the test for each property of want that the rule sel of
// rules does not set to its value. where names the layer and the media
// block of rules.
func wantDecls(t *testing.T, where string, rules map[string]map[string]string, sel string, want map[string]string) {
	t.Helper()
	got, ok := rules[sel]
	if !ok {
		t.Errorf("%s has no rule %s", where, sel)
		return
	}
	for prop, value := range want {
		if got[prop] != value {
			t.Errorf("%s: %s sets %s: %q, want %q", where, sel, prop, got[prop], value)
		}
	}
}

// Stamps follow DESIGN.md. A date stamp is Courier Prime at 15 px, a word
// stamp is Public Sans capitals at 12 px, and each one turns by one of
// three angles.
func TestStampsFollowTheDesign(t *testing.T) {
	top, _ := layerRules(t, "components")
	wantDecls(t, "components", top, ".stamp", map[string]string{
		"display":        "inline-block",
		"padding":        "4px 7px 3px",
		"border":         "1.5px solid currentColor",
		"border-radius":  "var(--r-stamp)",
		"color":          "var(--violet)",
		"font-family":    "var(--font-stamp)",
		"font-size":      "15px",
		"font-weight":    "700",
		"line-height":    "1",
		"letter-spacing": "0.03em",
		"mix-blend-mode": "var(--stamp-blend)",
	})
	wantDecls(t, "components", top, ".stamp-word", map[string]string{
		"padding":        "5px 7px 4px",
		"font-family":    "var(--font-sans)",
		"font-size":      "12px",
		"letter-spacing": "0.09em",
		"text-transform": "uppercase",
	})
	wantDecls(t, "components", top, ".stamp-approved", map[string]string{"color": "var(--violet)"})
	wantDecls(t, "components", top, ".stamp-denied", map[string]string{"color": "var(--ink)"})
	for sel, turn := range map[string]string{".turn-1": "-1.8deg", ".turn-2": "1.3deg", ".turn-3": "-1deg"} {
		wantDecls(t, "components", top, sel, map[string]string{"rotate": turn})
	}
}

// The One Motion Rule. A stamp that the viewer's own action made lands in
// two frames over 90 ms and never fades. Under prefers-reduced-motion it
// shows at once. Nothing else on the page moves.
func TestStampLandsInTwoFrames(t *testing.T) {
	top, media := layerRules(t, "components")
	wantDecls(t, "components", top, ".stamp-new", map[string]string{"animation": "stamp-land 90ms steps(2, jump-none)"})
	wantDecls(t, "components", top, "from", map[string]string{"scale": "1.12", "rotate": "0deg"})
	if !regexp.MustCompile(`@keyframes\s+stamp-land\s*\{\s*from\s*\{`).MatchString(appCSS(t)) {
		t.Error("app.css has no @keyframes stamp-land that starts with its from frame")
	}
	wantDecls(t, "components (prefers-reduced-motion: reduce)", media["(prefers-reduced-motion: reduce)"], ".stamp-new", map[string]string{"animation": "none"})
	for _, r := range cssRuleSets(t) {
		sel := strings.Join(r.sels, ", ")
		for prop := range r.decls {
			if (prop == "animation" || strings.HasPrefix(prop, "animation-")) && sel != ".stamp-new" {
				t.Errorf("%s sets %s, but the stamp is the only motion", sel, prop)
			}
		}
	}
}

// The Turned Stamp Rule. Only stamps rotate: a rotate, transform or scale
// sits only on the three turns and on the first frame of the landing.
// text-transform and translate are other properties.
func TestOnlyStampsTurn(t *testing.T) {
	allowed := map[string]bool{".turn-1": true, ".turn-2": true, ".turn-3": true, "from": true}
	found := 0
	for _, r := range cssRuleSets(t) {
		for _, prop := range []string{"rotate", "transform", "scale"} {
			if _, ok := r.decls[prop]; !ok {
				continue
			}
			found++
			for _, sel := range r.sels {
				if !allowed[sel] {
					t.Errorf("%s sets %s, but only stamps turn", strings.Join(r.sels, ", "), prop)
					break
				}
			}
		}
	}
	if found < 5 {
		t.Errorf("found %d rotate, transform and scale declarations, want at least 5: the three turns and the two of the first frame", found)
	}
}

// The Flat Desk Rule. No surface has a shadow.
func TestNoShadowOnAnySurface(t *testing.T) {
	if strings.Contains(appCSS(t), "box-shadow") {
		t.Error("app.css sets a box-shadow, but every surface is flat")
	}
}

// The Five Sizes Rule. Text is 19, 17, 16, 14, 13.5 or 12 px. Stamps are
// marks, so the date stamp is the one rule at 15 px.
func TestTextKeepsTheFiveSizes(t *testing.T) {
	sizes := map[string]bool{"19px": true, "17px": true, "16px": true, "14px": true, "13.5px": true, "12px": true, "inherit": true}
	found := 0
	for _, r := range cssRuleSets(t) {
		size, ok := r.decls["font-size"]
		if !ok {
			continue
		}
		found++
		sel := strings.Join(r.sels, ", ")
		if size == "15px" && sel == ".stamp" {
			continue
		}
		if !sizes[size] {
			t.Errorf("%s sets font-size %s, which is not one of the five sizes", sel, size)
		}
	}
	if found == 0 {
		t.Fatal("app.css has no font-size declaration")
	}
}

// Above 640 px the bulk bar sticks to the top of the window. The root keeps
// the height of the bar and 8 px clear, so a focused control and its ring
// never scroll under it. At every width the root keeps 8 px clear above and
// below a focused control, so its ring never ends past the window. On a
// phone the bar scrolls with the queue, and a long list in it is no scroll
// box.
func TestDeskBarSticksAboveThePhone(t *testing.T) {
	tokens, _ := layerRules(t, "tokens")
	wantDecls(t, "tokens", tokens, ":root", map[string]string{"--bar-h": "48px"})
	top, media := layerRules(t, "components")
	wantDecls(t, "components", top, ".desk-bar", map[string]string{"position": "sticky", "top": "0", "background": "var(--card)"})
	wantDecls(t, "components (max-width: 640px)", media["(max-width: 640px)"], ".desk-bar", map[string]string{"position": "static", "max-height": "none", "overflow-y": "visible"})
	layoutTop, layout := layerRules(t, "layout")
	wantDecls(t, "layout", layoutTop, ":root", map[string]string{"scroll-padding-top": "var(--space-sm)", "scroll-padding-bottom": "var(--space-sm)"})
	wantDecls(t, "layout (min-width: 641px)", layout["(min-width: 641px)"], ":root", map[string]string{"scroll-padding-top": "calc(var(--bar-h) + var(--space-sm))"})
	wantDecls(t, "components", top, ".slip", map[string]string{"scroll-margin-top": "var(--space-sm)"})
}

// The Card Stock Is the Desk Rule. The approval desk is card stock with
// card rules, dashed between the slips. Hover and selection use the card
// fills. The checked box holds the selection, never a class, so a morph
// keeps it. Controls on card stock take a pencil-grey border.
func TestDeskIsCardStock(t *testing.T) {
	top, _ := layerRules(t, "components")
	wantDecls(t, "components", top, ".desk", map[string]string{"background": "var(--card)", "border-color": "var(--card-rule)"})
	wantDecls(t, "components", top, ".desk .slip", map[string]string{"border-top": "1px dashed var(--card-rule)"})
	wantDecls(t, "components", top, ".desk .slip:hover", map[string]string{"background": "var(--card-hover)"})
	wantDecls(t, "components", top, ".desk .slip:has(> .slip-pick input:checked)", map[string]string{"background": "var(--card-sel)"})
	wantDecls(t, "components", top, ".desk-empty", map[string]string{
		"background":    "var(--card)",
		"border":        "1px solid var(--card-rule)",
		"border-radius": "var(--r-note)",
		"padding":       "10px var(--space-md)",
	})
	wantDecls(t, "components", top, ".slip:last-child", map[string]string{"border-radius": "0 0 calc(var(--r-panel) - 1px) calc(var(--r-panel) - 1px)"})
	for _, r := range cssRuleSets(t) {
		for _, sel := range r.sels {
			if strings.Contains(sel, ".selected") || strings.Contains(sel, ".is-selected") {
				t.Errorf("%s holds a selection in a class, which a morph loses", sel)
			}
		}
	}
	base, _ := layerRules(t, "base")
	wantDecls(t, "base", base, ":where(.desk) button", map[string]string{"border-color": "var(--ink-2)"})
	wantDecls(t, "base", base, ":where(.desk) input:not([type=checkbox]):not([type=radio]):not([type=hidden])", map[string]string{"border-color": "var(--ink-2)"})
}

// A slip that holds a refusal, and a slip that left the queue, get the
// dashed red-ink frame. A changed request gets the red-ink flag. The focus
// frame is a state in the last layer, so it beats the refused frame.
func TestRefusedSlipHasTheRedInkFrame(t *testing.T) {
	top, media := layerRules(t, "components")
	wantDecls(t, "components", top, `.slip[data-state="pending"][data-result], .slip[data-state="gone"]`, map[string]string{"outline": "1px dashed var(--red-ink)"})
	wantDecls(t, "components", top, ".flag-changed", map[string]string{"background": "var(--red-ink)", "color": "var(--paper)"})
	utilities, _ := layerRules(t, "utilities")
	wantDecls(t, "utilities", utilities, ".slip:focus-within", map[string]string{"outline": "2px solid var(--violet)"})
	blocks := []map[string]map[string]string{top}
	for _, rules := range media {
		blocks = append(blocks, rules)
	}
	for _, rules := range blocks {
		for key := range rules {
			for _, sel := range splitSelectors(key) {
				if sel == ".slip:focus-within" {
					t.Errorf("the components layer has the focus frame in %s, where the refused frame can beat it", key)
				}
			}
		}
	}
}

// The facts that decide stay beside Approve at every width. They are 16 px
// bold in row 1 above 1000 px, and one line under the purpose at 1000 px
// and below. There the labels and the values share the last line, so an old
// value sits above its new value and the line stays even. The opened parts,
// the note and the outcome take the whole row.
func TestFactsStayBesideApprove(t *testing.T) {
	top, media := layerRules(t, "components")
	wantDecls(t, "components", top, ".fact-now", map[string]string{"font-size": "16px", "font-weight": "700"})
	wantDecls(t, "components", top, ".slip-actions", map[string]string{"grid-column": "3", "grid-row": "1"})
	wantDecls(t, "components", top, ".slip-note, .slip-outcome, .slip-details, .slip-tools", map[string]string{"grid-column": "1 / -1"})
	narrow := media["(max-width: 1000px)"]
	wantDecls(t, "components (max-width: 1000px)", narrow, ".slip-facts", map[string]string{"display": "flex", "grid-column": "1", "grid-row": "2", "align-items": "last baseline"})
	wantDecls(t, "components (max-width: 1000px)", narrow, ".fact", map[string]string{"display": "flex", "align-items": "last baseline"})
	wantDecls(t, "components (max-width: 1000px)", narrow, ".slip-actions", map[string]string{"grid-row": "1 / span 2"})
}

// A request name can have 50 letters and no hyphen. Each note that can hold
// a name breaks it anywhere, so a phone page never scrolls sideways.
func TestNotesBreakALongName(t *testing.T) {
	top, _ := layerRules(t, "components")
	for _, sel := range []string{".notice", ".refusal", ".slip-note"} {
		wantDecls(t, "components", top, sel, map[string]string{"overflow-wrap": "anywhere"})
	}
}

// The hidden attribute closes the two opened parts of a slip. An author
// display value beats that attribute. So the details never set display,
// and the tools set it only while they show.
func TestOpenedPartsCloseWithHidden(t *testing.T) {
	var tools []string
	for _, r := range cssRuleSets(t) {
		if _, ok := r.decls["display"]; !ok {
			continue
		}
		sel := strings.Join(r.sels, ", ")
		if strings.Contains(sel, ".slip-details") {
			t.Errorf("%s sets display, so a closed slip shows its details", sel)
		}
		if strings.Contains(sel, ".slip-tools") {
			tools = append(tools, sel)
		}
	}
	if len(tools) != 1 || tools[0] != ".slip-tools:not([hidden])" {
		t.Errorf("the rules that name .slip-tools and set display are %q, want only .slip-tools:not([hidden])", tools)
	}
}

// On a phone Approve and Deny are 52 px tall with a 16 px bold label, and
// Approve takes three fifths of the row. The actions keep the stamp, the
// chip and the version at their own width. Only the button form of a slip
// name is a 44 px target.
func TestPhoneDecideButtons(t *testing.T) {
	_, utilities := layerRules(t, "utilities")
	phone := utilities["(max-width: 640px)"]
	const where = "utilities (max-width: 640px)"
	wantDecls(t, where, phone, ".slip-decide button", map[string]string{"height": "52px", "font-size": "16px", "font-weight": "700"})
	wantDecls(t, where, phone, "button.slip-name", map[string]string{"height": "auto"})
	wantDecls(t, where, phone, ".nav-link, .check, .brand, a.link, td a, button.slip-name", map[string]string{"min-height": "var(--touch)"})
	for key := range phone {
		for _, sel := range splitSelectors(key) {
			if sel == ".slip-name" {
				t.Errorf("%s: %s makes the name of a decided slip a 44 px target too", where, key)
			}
		}
	}
	_, components := layerRules(t, "components")
	cphone := components["(max-width: 640px)"]
	wantDecls(t, "components (max-width: 640px)", cphone, ".slip-actions", map[string]string{"align-items": "flex-start"})
	wantDecls(t, "components (max-width: 640px)", cphone, ".slip-decide", map[string]string{"grid-template-columns": "minmax(0, 3fr) minmax(0, 2fr)", "align-self": "stretch"})
}
