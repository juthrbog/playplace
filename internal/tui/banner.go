package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The banner is the tool's mark for the terminal: a cloud that is a ball
// pit. It is drawn from a template where block characters take the cloud
// colour and each ball takes the next colour in the pit. Glyphs alone carry
// the picture, so NO_COLOR terminals still get a cloud and dots.
// S marks the tube slide, M its mouth, both in the slide colour.
var bannerArt = []string{
	`           ▄▄▄▄▄▄▄▄           `,
	`       ▄▄▄██████████▄▄▄       `,
	`    ▄▄██████████████████SS▄   `,
	`   ███████████████████SS█████ `,
	`  ██ ● ● ● ● ● ● ● ●SS● ● ██ `,
	`  ▀██ ● ● ● ● ● ● ● M ● ● ██▀ `,
	`    ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀    `,
}

const bannerArtWidth = 30

// bannerBalls are the pit colours, cycled left to right, top to bottom.
func (t theme) bannerBalls() []color.Color {
	return []color.Color{t.ballRed, t.ballYellow, t.ballBlue, t.ballGreen, t.primary}
}

// bannerLines renders the art, or nil when the terminal is too narrow.
func (m model) bannerLines() []string {
	if m.width < bannerArtWidth+2 {
		return nil
	}
	th := m.th
	cloud := lipgloss.NewStyle().Foreground(th.cloud)
	slide := lipgloss.NewStyle().Foreground(th.ballRed)
	balls := th.bannerBalls()
	i := 0
	out := make([]string, 0, len(bannerArt))
	for _, row := range bannerArt {
		var b strings.Builder
		var run strings.Builder
		flush := func() {
			if run.Len() > 0 {
				b.WriteString(cloud.Render(run.String()))
				run.Reset()
			}
		}
		for _, r := range row {
			switch r {
			case '●':
				flush()
				b.WriteString(lipgloss.NewStyle().Foreground(balls[i%len(balls)]).Render("●"))
				i++
			case 'S':
				flush()
				b.WriteString(slide.Render("█"))
			case 'M':
				flush()
				b.WriteString(slide.Render("▓"))
			case ' ':
				flush()
				b.WriteRune(' ')
			default:
				run.WriteRune(r)
			}
		}
		flush()
		out = append(out, b.String())
	}
	return out
}

// banner is the art with the wordmark beside it when there is room, as a
// block of lines each exactly width cells wide, centred.
func (m model) banner(width int) []string {
	art := m.bannerLines()
	if art == nil {
		return nil
	}
	th := m.th
	words := []string{
		th.title.Render("playplace"),
		th.fact.Render("playground accounts with guardrails"),
		th.dialogHint.Render("the ball pit in the cloud"),
	}
	block := art
	if width >= bannerArtWidth+3+ansi.StringWidth(words[1]) {
		// Vertically centre the words beside the art.
		top := (len(art) - len(words)) / 2
		block = make([]string, len(art))
		for i, l := range art {
			w := ""
			if i >= top && i-top < len(words) {
				w = words[i-top]
			}
			block[i] = l + "   " + w
		}
	}
	var bw int
	for _, l := range block {
		if ansi.StringWidth(l) > bw {
			bw = ansi.StringWidth(l)
		}
	}
	left := (width - bw) / 2
	if left < 0 {
		left = 0
	}
	for i, l := range block {
		block[i] = pad(strings.Repeat(" ", left)+l, width)
	}
	return block
}
