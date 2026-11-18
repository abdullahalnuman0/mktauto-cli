package app

import (
	"os/exec"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "first release")
	run("tag", "v1.0.0")
	run("commit", "-q", "--allow-empty", "-m", "feat: faster video exports", "-m", "Exports use the GPU now")
	run("commit", "-q", "--allow-empty", "-m", "chore: bump deps")
	run("commit", "-q", "--allow-empty", "-m", "fix: captions keep their emoji")
	run("commit", "-q", "--allow-empty", "-m", "test: more export tests")
	run("tag", "v1.1.0")
	return dir
}

func TestDraftFromGit(t *testing.T) {
	repo := gitRepo(t)
	version, from, err := releaseRange(repo, "")
	if err != nil || version != "v1.1.0" || from != "v1.0.0" {
		t.Fatalf("range: %q %q %v", version, from, err)
	}
	changes, total, _ := changesSince(repo, from)
	if total != 4 || len(changes) != 2 || changes[0].subject != "fix: captions keep their emoji" {
		t.Fatalf("changes (internal ones left out): %d of %d %+v", len(changes), total, changes)
	}
	list := changeList("ReelWise", version, "https://reelwise.app", changes)
	if !strings.Contains(list, "Version: v1.1.0") || !strings.Contains(list, "(Exports use the GPU now)") {
		t.Errorf("list: %s", list)
	}
	plain := plainPost("ReelWise", version, "https://reelwise.app", changes)
	if !strings.HasPrefix(plain, "What's new in ReelWise 1.1.0:\n• Captions keep their emoji") || !strings.HasSuffix(plain, "https://reelwise.app") {
		t.Errorf("plain: %s", plain)
	}

	f, dir := setup(t)
	login(t, dir)
	if code, out := runCLI(t, dir, "draft", "--from-git", "--repo", repo, "--print"); code != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("print: %d %q", code, out)
	}
	code, out := runCLI(t, dir, "draft", "--from-git", "--repo", repo, "--app", "ReelWise", "--project", "Memoshot", "--accounts", "facebook")
	if code != 0 {
		t.Fatalf("draft: %d %s", code, out)
	}
	p := f.posts[0]
	if p["baseCaption"] != "Short one" || p["internalTitle"] != "ReelWise v1.1.0" || p["schedule"] != nil {
		t.Errorf("the AI's first draft, named after the release, saved as a draft: %v", p)
	}
	call := f.aiCalls[len(f.aiCalls)-1]
	if call["action"] != "RELEASE" || !strings.Contains(call["text"].(string), "feat: faster video exports") || strings.Contains(call["text"].(string), "bump deps") {
		t.Errorf("AI request: %v", call)
	}
	if code, out := runCLI(t, dir, "draft", "--from-git", "--repo", t.TempDir()); code == 0 || !strings.Contains(out, "isn't a git repository") {
		t.Errorf("not a repo: %s", out)
	}
	if code, _ := runCLI(t, dir, "draft"); code != exitUsage {
		t.Errorf("without --from-git: %d", code)
	}
}
