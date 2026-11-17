package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"rsc.io/qr"
)

// QR draws text as a QR code with half blocks (two modules per character row), black on white whatever the
// terminal's colours, with the quiet zone scanners need. Width in columns: QRWidth.
func QR(text string) (string, int, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", 0, err
	}
	const quiet = 2
	n := code.Size + 2*quiet
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < code.Size && y < code.Size && code.Black(x, y)
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#ffffff"))
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		var line strings.Builder
		for x := 0; x < n; x++ {
			top, bottom := dark(x, y), y+1 < n && dark(x, y+1)
			switch {
			case top && bottom:
				line.WriteRune('█')
			case top:
				line.WriteRune('▀')
			case bottom:
				line.WriteRune('▄')
			default:
				line.WriteRune(' ')
			}
		}
		b.WriteString(style.Render(line.String()) + "\n")
	}
	return b.String(), n, nil
}
