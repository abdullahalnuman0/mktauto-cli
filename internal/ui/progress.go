package ui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dustin/go-humanize"
)

// Progress shows one bar per file while uploading (Interactive), or a line per finished file.
type Progress struct {
	names []string
	sizes []int64
	prog  *tea.Program
	mu    sync.Mutex
	plain bool
	out   func(string)
	sent  []int64
	state []string
}

type progMsg struct {
	i     int
	sent  int64
	state string
}

type progModel struct {
	p    *Progress
	bars []progress.Model
	quit bool
}

func (m progModel) Init() tea.Cmd { return nil }

func (m progModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case progMsg:
		m.p.sent[v.i] = v.sent
		if v.state != "" {
			m.p.state[v.i] = v.state
		}
	case tea.QuitMsg:
		m.quit = true
	case tea.KeyMsg:
		if v.String() == "ctrl+c" {
			return m, tea.Interrupt
		}
	}
	return m, nil
}

func (m progModel) View() string {
	var b strings.Builder
	for i, name := range m.p.names {
		frac := 0.0
		if m.p.sizes[i] > 0 {
			frac = float64(m.p.sent[i]) / float64(m.p.sizes[i])
		}
		state := m.p.state[i]
		switch state {
		case "done":
			state = okS.Render("✓ uploaded")
		case "reused":
			state = okS.Render("✓ already stored")
		case "":
			state = fmt.Sprintf("%s / %s", humanize.Bytes(uint64(m.p.sent[i])), humanize.Bytes(uint64(m.p.sizes[i])))
		default:
			state = badS.Render(state)
		}
		b.WriteString(fmt.Sprintf("%s %s %s\n", pad(name, 28), m.bars[i].ViewAs(min(frac, 1)), state))
	}
	return b.String()
}

// StartProgress begins showing the files; call Done when finished.
func StartProgress(names []string, sizes []int64, interactive bool, out func(string)) *Progress {
	p := &Progress{names: names, sizes: sizes, plain: !interactive, out: out, sent: make([]int64, len(names)), state: make([]string, len(names))}
	if interactive {
		bars := make([]progress.Model, len(names))
		for i := range bars {
			bars[i] = progress.New(progress.WithDefaultGradient(), progress.WithWidth(30))
		}
		p.prog = tea.NewProgram(progModel{p: p, bars: bars})
		go func() { _, _ = p.prog.Run() }()
	}
	return p
}

// Update: bytes sent for file i.
func (p *Progress) Update(i int, sent int64) {
	if p.prog != nil {
		p.prog.Send(progMsg{i: i, sent: sent})
	}
}

// Finish marks file i: "done", "reused" or an error message.
func (p *Progress) Finish(i int, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.prog != nil {
		sent := p.sizes[i]
		if state != "done" && state != "reused" {
			sent = p.sent[i]
		}
		p.prog.Send(progMsg{i: i, sent: sent, state: state})
		return
	}
	switch state {
	case "done":
		p.out(fmt.Sprintf("✓ %s uploaded (%s)", p.names[i], humanize.Bytes(uint64(p.sizes[i]))))
	case "reused":
		p.out(fmt.Sprintf("✓ %s already stored", p.names[i]))
	default:
		p.out(fmt.Sprintf("✗ %s: %s", p.names[i], state))
	}
}

func (p *Progress) Done() {
	if p.prog != nil {
		p.prog.Quit()
		p.prog.Wait()
	}
}
