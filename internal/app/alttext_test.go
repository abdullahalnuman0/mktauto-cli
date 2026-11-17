package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-080 in the terminal: alt text per image (typed or by AI) and threads of replies on X / Threads.
func TestAltTextAndThreads(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook,instagram", "--files", "a.png,b.png",
		"--caption", "Two shots", "--alt", "a.png=The editor on a phone", "--alt-ai", "--yes")
	if code != 0 {
		t.Fatalf("post: %d %s", code, out)
	}
	alt := f.posts[0]["altTexts"].(map[string]any)
	ids := map[string]string{}
	for i, u := range f.uploads {
		ids[u["filename"].(string)] = fmt.Sprintf("asset-%d", i+1)
	}
	if alt[ids["a.png"]] != "The editor on a phone" || alt[ids["b.png"]] != "A phone showing the app" || len(f.aiCalls) != 1 {
		t.Errorf("typed alt for a.png, AI for b.png only: %v (ai calls %d)", alt, len(f.aiCalls))
	}
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--files", "c.png", "--alt", "nope.png=x", "--caption", "x"); code == 0 || !strings.Contains(out, "isn't one of the post's images") {
		t.Errorf("unknown file: %s", out)
	}

	// An X account isn't in the fake project: Threads-style splitting is checked through the plan (unit) and the
	// caption file with --- lines through Instagram (not a thread platform: kept as one caption).
	_ = os.WriteFile(filepath.Join(dir, "own.txt"), []byte("Part one\n---\nPart two"), 0o600)
	code, out = runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "instagram", "--files", "c.png", "--caption", "Main",
		"--caption-file-for", "instagram=own.txt", "--duplicates", "post", "--yes")
	if code != 0 {
		t.Fatalf("instagram: %d %s", code, out)
	}
	d := f.posts[1]["destinations"].([]any)[0].(map[string]any)
	if d["options"] != nil || !strings.Contains(d["caption"].(string), "---") {
		t.Errorf("no thread on Instagram: %v", d)
	}
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "x", "--thread-for", "facebook"); code == 0 || !strings.Contains(out, "isn't X or Threads") {
		t.Errorf("--thread-for on Facebook: %s", out)
	}
}
