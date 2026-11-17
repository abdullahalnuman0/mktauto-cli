package app

import (
	"errors"

	"github.com/abdullahalnuman0/mktauto-cli/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// errBack: the user pressed Esc to return to the previous question.
var errBack = errors.New("back")

// asker shows the wizard's questions. Every question counts, so the step runner knows which steps asked something
// (only those are places to go back to).
type asker struct {
	asked int
	// skip > 0: going back into a step that asked several questions. Its earlier questions are passed silently
	// (their answers are kept in the values they're bound to) so the user lands on the step's last question.
	skip int
	run  func(f *huh.Form) (back bool, err error) // replaceable in tests
}

func newAsker() *asker { return &asker{run: runBackable} }

// form asks the fields as one screen. Esc returns errBack; Ctrl+C cancels.
func (a *asker) form(fields ...huh.Field) error {
	return a.groups(huh.NewGroup(fields...))
}

func (a *asker) groups(groups ...*huh.Group) error {
	a.asked++
	if a.skip > 0 {
		a.skip--
		return nil
	}
	back, err := a.run(huh.NewForm(groups...).WithShowHelp(true))
	if err != nil {
		return err
	}
	if back {
		return errBack
	}
	return nil
}

// screen runs one of the tool's own screens (ui.Order, ui.Grid) and maps its Esc to errBack.
func (a *asker) screen(show func() error) error {
	a.asked++
	if a.skip > 0 {
		a.skip--
		return nil
	}
	return uiErr(show())
}

// replaying: a question about to be passed silently (see skip).
func (a *asker) replaying() bool { return a.skip > 0 }

// uiErr maps the tool's own screens' Esc and Ctrl+C to the wizard's.
func uiErr(err error) error {
	switch {
	case errors.Is(err, ui.ErrBack):
		return errBack
	case errors.Is(err, ui.ErrCancelled):
		return huh.ErrUserAborted
	}
	return err
}

// backable wraps a form so Esc means "back" (huh itself treats Esc and Ctrl+C alike).
type backable struct {
	form *huh.Form
	back bool
}

func (m *backable) Init() tea.Cmd { return m.form.Init() }

func (m *backable) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
		m.back = true
		return m, tea.Quit
	}
	f, cmd := m.form.Update(msg)
	if ff, ok := f.(*huh.Form); ok {
		m.form = ff
	}
	return m, cmd
}

func (m *backable) View() string {
	if m.back || m.form.State != huh.StateNormal {
		return ""
	}
	return m.form.View() + "\n" + faint.Render("esc back · ctrl+c cancel") + "\n"
}

func runBackable(f *huh.Form) (bool, error) {
	f.SubmitCmd = tea.Quit
	f.CancelCmd = tea.Quit
	m := &backable{form: f}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return false, err
	}
	if m.back {
		return true, nil
	}
	if m.form.State == huh.StateAborted {
		return false, huh.ErrUserAborted
	}
	return false, nil
}

// step is one part of the wizard. A step reads what earlier steps decided and sets only its own results, so it
// can run again after the user went back.
type step struct {
	name string
	run  func() error
}

// runSteps runs the steps in order. On errBack it returns to the last question of the last step that asked
// something (steps that only computed something run again on the way forward). Back from the first question:
// nested runs return errBack to their caller; the outermost run asks the first question again.
func runSteps(a *asker, steps []step, outermost bool) error {
	var asked []int        // steps that asked something, in order
	count := map[int]int{} // questions each of them asked
	start := map[int]int{} // a.asked before each step
	replayed := -1         // the step this run went back into (its earlier questions pass silently)
	for i := 0; i < len(steps); {
		start[i] = a.asked
		err := steps[i].run()
		if errors.Is(err, errBack) {
			if len(asked) == 0 {
				a.asked = start[i]
				if outermost {
					continue // nothing before this: ask again
				}
				return errBack
			}
			i, asked = asked[len(asked)-1], asked[:len(asked)-1]
			// Questions after the start of step i are being redone: the count follows the path the user took.
			a.asked = start[i]
			a.skip = count[i] - 1
			replayed = i
			continue
		}
		if err != nil {
			return err
		}
		if i == replayed {
			replayed = -1
			if a.skip > 0 {
				// The step asked fewer questions this time than before: show it from its first question.
				a.skip, a.asked = 0, start[i]
				continue
			}
		}
		if a.asked > start[i] {
			asked = append(asked, i)
			count[i] = a.asked - start[i]
		}
		i++
	}
	return nil
}
