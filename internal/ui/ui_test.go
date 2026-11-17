package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestOrderMovesFilesWithTheKeyboard(t *testing.T) {
	var m tea.Model = orderModel{heading: "Order", labels: []string{"a", "b", "c", "d"}, order: []int{0, 1, 2, 3}}
	for _, k := range []string{"down", "down", "u", "t", "down", "down", "d"} {
		m, _ = m.Update(key(k))
	}
	// c moved up, then to the top: c a b d; then b moved down past d: c a d b.
	if got := fmt.Sprint(m.(orderModel).order); got != "[2 0 3 1]" {
		t.Fatalf("order %s", got)
	}
	m, _ = m.Update(key("enter"))
	if !m.(orderModel).done {
		t.Error("enter finishes")
	}
}

func TestGridTicksFilesPerAccountAndShowsTheResult(t *testing.T) {
	sel := [][]bool{{true, true}, {true, true}, {true, true}}
	verdicts := []string{}
	var m tea.Model = gridModel{rows: []string{"1.png", "2.png", "3.png"}, cols: []Column{{Short: "FB"}, {Short: "TikTok"}}, sel: sel, reorder: -1,
		verdict: func(col int, rows []int) Verdict {
			verdicts = append(verdicts, fmt.Sprint(col, rows))
			return Verdict{Label: fmt.Sprintf("%d files", len(rows)), OK: len(rows) > 0}
		}}
	for _, k := range []string{"right", "f", "left", "down", "down", "space"} {
		m, _ = m.Update(key(k))
	}
	g := m.(gridModel)
	// TikTok: first file only; Facebook: the 3rd file unticked.
	if fmt.Sprint(g.ticked(1)) != "[0]" || fmt.Sprint(g.ticked(0)) != "[0 1]" {
		t.Fatalf("FB %v TikTok %v", g.ticked(0), g.ticked(1))
	}
	_ = g.View()
	if len(verdicts) == 0 {
		t.Error("each column shows what it will post")
	}
	m, _ = m.Update(key("a")) // whole Facebook column on
	if fmt.Sprint(m.(gridModel).ticked(0)) != "[0 1 2]" {
		t.Error("a ticks the whole column")
	}
	m, _ = m.Update(key("right"))
	m, _ = m.Update(key("o"))
	if m.(gridModel).reorder != 1 {
		t.Error("o asks to reorder the current account's files")
	}
}

func TestLogoAndQR(t *testing.T) {
	for _, row := range logoRows() {
		if len([]rune(row)) != LogoWidth {
			t.Fatalf("logo rows must have the same width: %q", row)
		}
	}
	if LogoWidth > 78 {
		t.Errorf("the logo should fit an 80-column terminal: %d", LogoWidth)
	}
	q, w, err := QR("https://mktauto.site/cli/approve?code=BCDF-G7HK")
	if err != nil || w < 25 || strings.Count(q, "\n") != (w+1)/2 {
		t.Errorf("qr: %d columns, %d lines, %v", w, strings.Count(q, "\n"), err)
	}
}
