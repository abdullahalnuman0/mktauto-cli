package ui

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type spinDone struct{ err error }

type spinModel struct {
	s     spinner.Model
	title string
	err   error
	done  bool
}

func (m spinModel) Init() tea.Cmd { return m.s.Tick }

func (m spinModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinDone:
		m.err, m.done = msg.err, true
		return m, tea.Quit
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err, m.done = ErrCancelled, true
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.s, cmd = m.s.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m spinModel) View() string {
	if m.done {
		return ""
	}
	return m.s.View() + " " + m.title + "\n"
}

// Spin shows a spinner with title while work runs (in a real terminal; otherwise it just runs work).
func Spin(title string, interactive bool, work func() error) error {
	if !interactive {
		return work()
	}
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = cursorS
	p := tea.NewProgram(spinModel{s: s, title: title})
	go func() { p.Send(spinDone{work()}) }()
	res, err := p.Run()
	if err != nil {
		return err
	}
	return res.(spinModel).err
}
