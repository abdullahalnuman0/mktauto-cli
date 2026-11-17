package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The wordmark, one letter per entry (ANSI Shadow style): blocks are the letters, box-drawing characters their shadow.
var letters = [][6]string{
	{"███╗   ███╗", "████╗ ████║", "██╔████╔██║", "██║╚██╔╝██║", "██║ ╚═╝ ██║", "╚═╝     ╚═╝"}, // M
	{"██╗  ██╗", "██║ ██╔╝", "█████╔╝ ", "██╔═██╗ ", "██║  ██╗", "╚═╝  ╚═╝"},                         // K
	{"████████╗", "╚══██╔══╝", "   ██║   ", "   ██║   ", "   ██║   ", "   ╚═╝   "},                 // T
	{" █████╗ ", "██╔══██╗", "███████║", "██╔══██║", "██║  ██║", "╚═╝  ╚═╝"},                         // A
	{"██╗   ██╗", "██║   ██║", "██║   ██║", "██║   ██║", "╚██████╔╝", " ╚═════╝ "},                 // U
	{"████████╗", "╚══██╔══╝", "   ██║   ", "   ██║   ", "   ██║   ", "   ╚═╝   "},                 // T
	{" ██████╗ ", "██╔═══██╗", "██║   ██║", "██║   ██║", "╚██████╔╝", " ╚═════╝ "},                 // O
}

// LogoWidth is the wordmark's width in columns.
var LogoWidth = len([]rune(logoRows()[0]))

func logoRows() []string {
	rows := make([]string, 6)
	for _, l := range letters {
		for r := range rows {
			rows[r] += l[r]
		}
	}
	return rows
}

// Brand gradient: indigo (the website's brand-600) → violet → pink.
var stops = [][3]float64{{0x4f, 0x46, 0xe5}, {0x8b, 0x5c, 0xf6}, {0xec, 0x48, 0x99}}

func gradient(t float64) [3]float64 {
	t = math.Max(0, math.Min(1, t))
	seg := t * float64(len(stops)-1)
	i := min(int(seg), len(stops)-2)
	f := seg - float64(i)
	var c [3]float64
	for k := range c {
		c[k] = stops[i][k] + (stops[i+1][k]-stops[i][k])*f
	}
	return c
}

func hex(c [3]float64) lipgloss.Color {
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", int(c[0]), int(c[1]), int(c[2])))
}

func mix(c [3]float64, toward float64, f float64) [3]float64 {
	for k := range c {
		c[k] += (toward - c[k]) * f
	}
	return c
}

// renderLogo draws the wordmark: columns up to `shown` (a fraction 0..1) are visible, and a light band ("shine",
// a column position, < 0 = none) brightens what it passes over.
func renderLogo(shown, shine float64) string {
	rows := logoRows()
	width := float64(LogoWidth)
	var b strings.Builder
	for r, row := range rows {
		b.WriteString("  ")
		for x, ch := range []rune(row) {
			pos := float64(x) / width
			if pos > shown || ch == ' ' {
				b.WriteRune(' ')
				continue
			}
			// Each row is shifted a little, so the gradient runs diagonally.
			c := gradient(pos*0.9 + float64(r)*0.02)
			if ch != '█' {
				c = mix(c, 40, 0.45) // the shadow: darker
			}
			if shine >= 0 {
				if d := math.Abs(float64(x) - shine - float64(r)); d < 4 {
					c = mix(c, 255, 0.75*(1-d/4))
				}
			}
			b.WriteString(lipgloss.NewStyle().Foreground(hex(c)).Render(string(ch)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

type frame struct{}

type bannerModel struct {
	step, steps int
	tagline     string
}

func (m bannerModel) Init() tea.Cmd { return tick() }

func tick() tea.Cmd { return tea.Tick(28*time.Millisecond, func(time.Time) tea.Msg { return frame{} }) }

func (m bannerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case frame:
		m.step++
		if m.step >= m.steps {
			return m, tea.Quit
		}
		return m, tick()
	case tea.KeyMsg: // any key skips the animation
		m.step = m.steps
		return m, tea.Quit
	}
	return m, nil
}

// The first half reveals the letters left to right; the second sends a shine across them.
func (m bannerModel) View() string {
	half := m.steps / 2
	shown, shine := 1.0, -1.0
	if m.step < half {
		shown = easeOut(float64(m.step+1) / float64(half))
	} else if m.step < m.steps {
		shine = -6 + (float64(LogoWidth)+12)*float64(m.step-half)/float64(m.steps-half)
	}
	return "\n" + renderLogo(shown, shine) + m.tagline + "\n"
}

func easeOut(t float64) float64 { return 1 - math.Pow(1-t, 3) }

// Banner shows the wordmark with the tagline under it, animated when animate is true (about a second; a key skips
// it). Narrow terminals get a one-line title instead.
func Banner(tagline string, width int, animate bool) {
	if width > 0 && width < LogoWidth+4 {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(hex(gradient(0))).Render("mktauto") + "  " + tagline)
		return
	}
	if !animate {
		fmt.Print("\n" + renderLogo(1, -1) + tagline + "\n")
		return
	}
	m := bannerModel{steps: 36, tagline: tagline}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Print("\n" + renderLogo(1, -1) + tagline + "\n")
	}
}
