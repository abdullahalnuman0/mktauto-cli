package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Column is one account in the grid.
type Column struct {
	Short string // header, e.g. "IG @memoshot.app"
}

// Verdict for one column: what the account will post with the ticked files.
type Verdict struct {
	Label string // e.g. "Carousel (4)"
	OK    bool
}

type gridModel struct {
	rows    []string
	cols    []Column
	sel     [][]bool // [row][col]
	verdict func(col int, rows []int) Verdict
	r, c    int
	done    bool
	cancel  bool
	reorder int // column to reorder, -1 none
}

func (m gridModel) Init() tea.Cmd { return nil }

func (m gridModel) ticked(col int) []int {
	var out []int
	for r := range m.rows {
		if m.sel[r][col] {
			out = append(out, r)
		}
	}
	return out
}

func (m gridModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		if m.r > 0 {
			m.r--
		}
	case "down", "j":
		if m.r < len(m.rows)-1 {
			m.r++
		}
	case "left", "h", "shift+tab":
		if m.c > 0 {
			m.c--
		}
	case "right", "l", "tab":
		if m.c < len(m.cols)-1 {
			m.c++
		}
	case " ", "x":
		m.sel[m.r][m.c] = !m.sel[m.r][m.c]
	case "a": // the whole column on/off
		all := len(m.ticked(m.c)) == len(m.rows)
		for r := range m.rows {
			m.sel[r][m.c] = !all
		}
	case "f": // first file only
		for r := range m.rows {
			m.sel[r][m.c] = r == 0
		}
	case "r": // the whole row on/off
		all := true
		for c := range m.cols {
			all = all && m.sel[m.r][c]
		}
		for c := range m.cols {
			m.sel[m.r][c] = !all
		}
	case "o":
		m.reorder = m.c
		return m, tea.Quit
	case "enter":
		m.done = true
		return m, tea.Quit
	case "esc", "ctrl+c", "q":
		m.cancel = true
		return m, tea.Quit
	}
	return m, nil
}

func pad(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}

func (m gridModel) View() string {
	if m.done || m.cancel || m.reorder >= 0 {
		return ""
	}
	const nameW, colW = 26, 16
	var b strings.Builder
	b.WriteString(title.Render("Which files go to which account") + "\n")
	b.WriteString(strings.Repeat(" ", nameW+4))
	for c, col := range m.cols {
		h := pad(col.Short, colW)
		if c == m.c {
			h = cursorS.Render(h)
		}
		b.WriteString(h + " ")
	}
	b.WriteString("\n")
	for r, name := range m.rows {
		prefix := "  "
		if r == m.r {
			prefix = cursorS.Render("› ")
		}
		b.WriteString(prefix + fmt.Sprintf("%-2d", r+1) + pad(name, nameW))
		for c := range m.cols {
			cell := "  ·  "
			if m.sel[r][c] {
				cell = "  ✓  "
			}
			if r == m.r && c == m.c {
				cell = cursorS.Render("[" + strings.TrimSpace(cell) + "]")
				cell = "  " + cell + " "
			} else if m.sel[r][c] {
				cell = okS.Render(cell)
			} else {
				cell = dimS.Render(cell)
			}
			b.WriteString(pad("", 0) + cell + strings.Repeat(" ", colW-4))
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat(" ", nameW+4))
	for c := range m.cols {
		v := m.verdict(c, m.ticked(c))
		s := pad(v.Label, colW)
		if v.OK {
			s = okS.Render(s)
		} else {
			s = badS.Render(s)
		}
		b.WriteString(s + " ")
	}
	b.WriteString("\n" + help.Render("←/→ account · ↑/↓ file · space tick · a all/none · f first only · r whole row · o order for this account · enter done") + "\n")
	return b.String()
}

// Grid edits sel ([file][account]). It returns the column whose own order the user wants to change (or -1).
func Grid(rows []string, cols []Column, sel [][]bool, verdict func(col int, rows []int) Verdict) (int, error) {
	m := gridModel{rows: rows, cols: cols, sel: sel, verdict: verdict, reorder: -1}
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return -1, err
	}
	out := res.(gridModel)
	if out.cancel {
		return -1, ErrCancelled
	}
	return out.reorder, nil
}
