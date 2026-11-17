// Package ui holds the tool's own terminal screens (lists to reorder, the per-account grid, upload progress).
package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ErrCancelled: the user pressed Ctrl+C.
var ErrCancelled = errors.New("cancelled")

// ErrBack: the user pressed Esc (back to the previous step).
var ErrBack = errors.New("back")

var (
	title   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	help    = lipgloss.NewStyle().Faint(true)
	cursorS = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	okS     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	badS    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dimS    = lipgloss.NewStyle().Faint(true)
)

type orderModel struct {
	heading string
	labels  []string
	order   []int
	cursor  int
	done    bool
	cancel  bool
	back    bool
}

func (m orderModel) Init() tea.Cmd { return nil }

func (m orderModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	swap := func(a, b int) { m.order[a], m.order[b] = m.order[b], m.order[a] }
	switch k.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.order)-1 {
			m.cursor++
		}
	case "shift+up", "K", "ctrl+up", "u":
		if m.cursor > 0 {
			swap(m.cursor, m.cursor-1)
			m.cursor--
		}
	case "shift+down", "J", "ctrl+down", "d":
		if m.cursor < len(m.order)-1 {
			swap(m.cursor, m.cursor+1)
			m.cursor++
		}
	case "home", "t":
		if m.cursor > 0 {
			v := m.order[m.cursor]
			m.order = append([]int{v}, append(m.order[:m.cursor:m.cursor], m.order[m.cursor+1:]...)...)
			m.cursor = 0
		}
	case "enter":
		m.done = true
		return m, tea.Quit
	case "esc":
		m.back = true
		return m, tea.Quit
	case "ctrl+c":
		m.cancel = true
		return m, tea.Quit
	}
	return m, nil
}

func (m orderModel) View() string {
	if m.done || m.cancel || m.back {
		return ""
	}
	var b strings.Builder
	b.WriteString(title.Render(m.heading) + "\n")
	for pos, i := range m.order {
		line := fmt.Sprintf("%2d. %s", pos+1, m.labels[i])
		if pos == m.cursor {
			line = cursorS.Render("› " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(help.Render("↑/↓ choose · u/d (or shift+↑/↓) move · t to top · enter done · esc back") + "\n")
	return b.String()
}

// Order lets the user arrange items; returns their indexes in the new order.
func Order(heading string, labels []string) ([]int, error) {
	m := orderModel{heading: heading, labels: labels, order: make([]int, len(labels))}
	for i := range labels {
		m.order[i] = i
	}
	if len(labels) < 2 {
		return m.order, nil
	}
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, err
	}
	out := res.(orderModel)
	if out.cancel {
		return nil, ErrCancelled
	}
	if out.back {
		return nil, ErrBack
	}
	return out.order, nil
}
