package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCalendarRepeatDuplicateAndStopRepeat(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	at := time.Now().Add(48 * time.Hour).Format("2006-01-02") + " 10:00"
	code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "Weekly tip", "--when", at,
		"--repeat", "weekly", "--repeat-until", "2099-12-31", "--yes")
	if code != 0 {
		t.Fatalf("post: %d %s", code, out)
	}
	if rep := f.posts[0]["repeat"].(map[string]any); rep["everyDays"] != float64(7) || rep["until"] != "2099-12-31" {
		t.Errorf("repeat: %v", rep)
	}
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "x", "--repeat", "weekly"); code == 0 || !strings.Contains(out, "needs a time") {
		t.Errorf("repeat without a time: %s", out)
	}
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "x", "--when", at, "--repeat", "400"); code == 0 {
		t.Errorf("repeat out of range: %s", out)
	}
	code, out = runCLI(t, dir, "calendar", "--project", "Memoshot", "--days", "7")
	if code != 0 || !strings.Contains(out, "Weekly tip") || !strings.Contains(out, "↻ 7d") || !strings.Contains(out, "free posting time") {
		t.Errorf("calendar: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "duplicate", "last"); code != 0 || !strings.Contains(out, "Copied as a new draft") || len(f.posts) != 2 {
		t.Errorf("duplicate: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "stop-repeat", postID(1)); code != 0 || !strings.Contains(out, "won't repeat") {
		t.Errorf("stop-repeat: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "stop-repeat", postID(1)); code == 0 || !strings.Contains(out, "doesn't repeat") {
		t.Errorf("stop-repeat twice: %s", out)
	}
}

func TestStatsAndInbox(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "Launch")
	if code, out := runCLI(t, dir, "stats"); code != 0 || !strings.Contains(out, "120 likes") || !strings.Contains(out, "3,400 reach") || !strings.Contains(out, "Top posts") {
		t.Errorf("stats: %s", out)
	}
	if code, out := runCLI(t, dir, "stats", "last"); code != 0 || !strings.Contains(out, "12 likes · 3 comments") || !strings.Contains(out, "don't share numbers") {
		t.Errorf("post stats: %s", out)
	}
	if code, out := runCLI(t, dir, "stats", "--links"); code != 0 || !strings.Contains(out, "42") || !strings.Contains(out, "ig 30") {
		t.Errorf("links: %s", out)
	}
	if code, out := runCLI(t, dir, "inbox"); code != 0 || !strings.Contains(out, "Instagram failed") {
		t.Errorf("inbox: %s", out)
	}
	if code, _ := runCLI(t, dir, "inbox", "--read-all"); code != 0 || !f.readAll {
		t.Error("read-all")
	}
}

func TestPreviewInDryRun(t *testing.T) {
	_, dir := setup(t)
	login(t, dir)
	code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook,instagram", "--files", "a.png", "--caption", "Hello #android",
		"--hashtags", "android kotlin", "--caption-for", "instagram=Short", "--dry-run")
	if code != 0 || !strings.Contains(out, "Hello #android\n\n#kotlin") || !strings.Contains(out, "Short\n\n#android #kotlin") {
		t.Errorf("preview: the hashtags a caption already has aren't added again: %d %s", code, out)
	}
}

func TestCompletion(t *testing.T) {
	_, dir := setup(t)
	login(t, dir)
	t.Chdir(dir)
	e, err := newEnv("1.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	e.Server = os.Getenv("MKTAUTO_SERVER")
	e.Dir = dir
	values := func(args ...string) string {
		var out []string
		for _, c := range e.complete(context.Background(), args) {
			out = append(out, c.value)
		}
		return strings.Join(out, " ")
	}
	if got := values("po"); got != "post posts" {
		t.Errorf("commands: %q", got)
	}
	if got := values("post", "--proj"); got != "--project" {
		t.Errorf("flags: %q", got)
	}
	if got := values("post", "--project", ""); got != "Memoshot ReelWise" {
		t.Errorf("projects: %q", got)
	}
	if got := values("post", "--accounts", "facebook,gr"); got != `facebook,group:Launch` {
		t.Errorf("accounts list: %q", got)
	}
	if got := values("post", "--caption-for", "@q"); got != "@qirox=" {
		t.Errorf("per-account flag values end with =: %q", got)
	}
	if got := values("post", "--files", "a.png,"); !strings.Contains(got, "a.png,b.png") || strings.Contains(got, "a.png,a.png") {
		t.Errorf("files: %q", got)
	}
	if got := values("schedule", ""); !strings.HasPrefix(got, "last") {
		t.Errorf("posts: %q", got)
	}
	if got := values("completion", "z"); got != "zsh" {
		t.Errorf("shells: %q", got)
	}
	// Every command's flags are the ones its -h shows.
	for cmd, names := range commandFlags {
		_, help := runCLI(t, dir, cmd, "-h")
		for _, n := range names {
			if !strings.Contains(help, "-"+n) {
				t.Errorf("%s -h doesn't show -%s:\n%s", cmd, n, help)
			}
		}
		if cmd == "batch" || cmd == "import" {
			continue // their help lists mktauto post's flags too
		}
		for _, line := range strings.Split(help, "\n") {
			if f := strings.Fields(line); len(f) > 0 && strings.HasPrefix(f[0], "-") && strings.HasPrefix(line, "  -") {
				name := strings.TrimPrefix(f[0], "-")
				if !contains(names, name) {
					t.Errorf("%s has -%s, missing from its completion list", cmd, name)
				}
			}
		}
	}
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		if code, out := runCLI(t, dir, "completion", sh); code != 0 || !strings.Contains(out, "__complete --shell="+sh) {
			t.Errorf("%s script: %s", sh, out)
		}
	}
	_ = filepath.Join
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
