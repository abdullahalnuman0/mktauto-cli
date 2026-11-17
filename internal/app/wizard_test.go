package app

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
)

// Esc goes back one question at a time, across steps and into steps with several questions; steps that only
// compute something run again on the way forward.
func TestBackGoesToThePreviousQuestion(t *testing.T) {
	answers := strings.Fields("ok ok ok back back back ok ok ok ok") // A C1 C2 D(back) C2(back) C1(back) A C1 C2 D
	a := newAsker()
	var shown []string
	current := ""
	a.run = func(*huh.Form) (bool, error) {
		shown = append(shown, current)
		next := answers[0]
		answers = answers[1:]
		return next == "back", nil
	}
	ask := func(name string) error {
		current = name
		return a.form(huh.NewNote())
	}
	computed := 0
	steps := []step{
		{run: func() error { return ask("A") }},
		{run: func() error { computed++; return nil }},
		{run: func() error {
			return runSteps(a, []step{{run: func() error { return ask("C1") }}, {run: func() error { return ask("C2") }}}, false)
		}},
		{run: func() error { return ask("D") }},
	}
	if err := runSteps(a, steps, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(shown, " "); got != "A C1 C2 D C2 C1 A C1 C2 D" {
		t.Errorf("questions shown: %s", got)
	}
	if computed != 2 {
		t.Errorf("the computing step ran %d times, want 2", computed)
	}
	if len(answers) != 0 {
		t.Errorf("unused answers: %v", answers)
	}
}

// Back on the very first question leaves the wizard (the menu shows again); Ctrl+C cancels.
func TestBackFromTheFirstQuestionAndCancel(t *testing.T) {
	a := newAsker()
	a.run = func(*huh.Form) (bool, error) { return true, nil }
	if err := runSteps(a, []step{{run: func() error { return a.form(huh.NewNote()) }}}, false); !errors.Is(err, errBack) {
		t.Errorf("back from the first question: %v", err)
	}
	a.run = func(*huh.Form) (bool, error) { return false, huh.ErrUserAborted }
	if err := runSteps(a, []step{{run: func() error { return a.form(huh.NewNote()) }}}, false); !errors.Is(err, huh.ErrUserAborted) {
		t.Errorf("ctrl+c: %v", err)
	}
}

// TestServeFake serves the fake API for trying the wizard by hand (or from a script) in a real terminal:
//
//	MKTAUTO_FAKE_SERVE=/tmp/fake go test ./internal/app -run TestServeFake -timeout 0
//
// then run mktauto with MKTAUTO_SERVER=$(cat /tmp/fake) MKTAUTO_TOKEN=mkc_session. Create /tmp/fake.stop to end it;
// the posts it received are written to /tmp/fake.posts.
func TestServeFake(t *testing.T) {
	file := os.Getenv("MKTAUTO_FAKE_SERVE")
	if file == "" {
		t.Skip("only when MKTAUTO_FAKE_SERVE is set")
	}
	f := newFakeServer(t)
	if err := os.WriteFile(file, []byte(f.srv.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Hour); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if _, err := os.Stat(file + ".stop"); err == nil {
			break
		}
	}
	f.mu.Lock()
	data, _ := json.MarshalIndent(f.posts, "", "  ")
	f.mu.Unlock()
	_ = os.WriteFile(file+".posts", data, 0o600)
}
