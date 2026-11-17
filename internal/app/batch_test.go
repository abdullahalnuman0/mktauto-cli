package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func login(t *testing.T, dir string) {
	t.Helper()
	if code, out := runCLI(t, dir, "login", "--code", "mkl_good"); code != 0 {
		t.Fatalf("login: %s", out)
	}
}

// One post per file into the project's next free posting times; each file's .txt is its caption; accounts that
// can't take a post are left out of that one.
func TestBatchIntoNextFreeSlots(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("Caption from b.txt"), 0o600)
	code, out := runCLI(t, dir, "batch", "--each", "file", "--project", "Memoshot", "--accounts", "instagram,tiktok", "--caption", "Default for {name}",
		"--when", "next-slot", "--tiktok-privacy", "SELF_ONLY", "--limit", "3", "--yes")
	if code != 0 {
		t.Fatalf("batch: %d %s", code, out)
	}
	if len(f.posts) != 3 || len(f.scheduled) != 3 {
		t.Fatalf("3 posts scheduled (the 4th is over --limit): %d posts, %d scheduled\n%s", len(f.posts), len(f.scheduled), out)
	}
	captions := []string{f.posts[0]["baseCaption"].(string), f.posts[1]["baseCaption"].(string), f.posts[2]["baseCaption"].(string)}
	if captions[0] != "Default for a" || captions[1] != "Caption from b.txt" {
		t.Errorf("captions: %v", captions)
	}
	sched := f.posts[2]["schedule"].(map[string]any)
	if sched["localDateTime"] != time.Now().Add(72*time.Hour).Format("2006-01-02")+"T09:00:00" {
		t.Errorf("third post in the third free time: %v", sched)
	}
	if !strings.Contains(out, "over --limit") || !strings.Contains(out, "3/3") {
		t.Errorf("plan and progress lines: %s", out)
	}
	// Run again: the files posted from this folder are skipped.
	code, out = runCLI(t, dir, "batch", "--each", "image", "--project", "Memoshot", "--accounts", "instagram", "--when", "draft", "--yes")
	if code != 0 || len(f.posts) != 3 || !strings.Contains(out, "posted before") {
		t.Errorf("posted files are skipped: %d posts\n%s", len(f.posts), out)
	}
	if code, out := runCLI(t, dir, "batch", "--each", "file", "--project", "Memoshot", "--accounts", "instagram", "--when", "next-slot",
		"--include-posted", "--yes"); code == 0 || !strings.Contains(out, "free posting times") {
		t.Errorf("more posts than free times: %s", out)
	}
	if code, out := runCLI(t, dir, "batch", "--each", "image", "--project", "Memoshot", "--accounts", "instagram", "--when", "now", "--yes"); code == 0 {
		t.Errorf("--when now is refused: %s", out)
	}
}

func TestBatchFromAStartTimeEvery(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	code, out := runCLI(t, dir, "batch", "--each", "image", "--project", "Memoshot", "--accounts", "facebook", "--caption", "x",
		"--when", "2099-05-01 10:00", "--every", "1d12h", "--dry-run")
	if code != 0 || len(f.posts) != 0 || !strings.Contains(out, "2099-05-02 22:00") || !strings.Contains(out, "2099-05-04 10:00") {
		t.Errorf("dry run plan: %d %s", code, out)
	}
	if _, err := parseEvery("5m"); err == nil {
		t.Error("too short")
	}
	if d, err := parseEvery("2d6h"); err != nil || d != 54*time.Hour {
		t.Errorf("2d6h: %v %v", d, err)
	}
}

func TestImportCSV(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	csv := "\uFEFFfiles,caption,accounts,when,title\n" +
		"a.png;b.png,\"Two images, one post\",facebook;instagram,2099-02-01 10:00,\n" +
		"reel.mp4,A reel,instagram,next-slot,Reel title\n" +
		",Text only,facebook,,\n"
	_ = os.WriteFile(filepath.Join(dir, "plan.csv"), []byte(csv), 0o600)
	code, out := runCLI(t, dir, "import", "plan.csv", "--project", "Memoshot", "--yes")
	if code != 0 {
		t.Fatalf("import: %d %s", code, out)
	}
	if len(f.posts) != 3 || f.posts[0]["baseCaption"] != "Two images, one post" || len(f.posts[0]["mediaIds"].([]any)) != 2 {
		t.Fatalf("posts: %v", f.posts)
	}
	if s := f.posts[1]["schedule"].(map[string]any); s["localDateTime"] != time.Now().Add(24*time.Hour).Format("2006-01-02")+"T09:00:00" {
		t.Errorf("next-slot row: %v", s)
	}
	if f.posts[2]["schedule"] != nil {
		t.Errorf("a row without a time is a draft: %v", f.posts[2]["schedule"])
	}
	// Problems are listed before anything is saved.
	_ = os.WriteFile(filepath.Join(dir, "bad.csv"), []byte("files,accounts,when\nmissing.png,facebook,draft\nb.png,,now\n"), 0o600)
	code, out = runCLI(t, dir, "import", "bad.csv", "--project", "Memoshot", "--yes")
	if code == 0 || !strings.Contains(out, "row 2: missing.png") || !strings.Contains(out, "row 3: no accounts") || !strings.Contains(out, "row 3: \"now\"") || len(f.posts) != 3 {
		t.Errorf("bad rows: %d %s", code, out)
	}
	_ = os.WriteFile(filepath.Join(dir, "odd.csv"), []byte("files,colour\na.png,red\n"), 0o600)
	if code, out := runCLI(t, dir, "import", "odd.csv"); code == 0 || !strings.Contains(out, "unknown column") {
		t.Errorf("unknown column: %s", out)
	}
	if code, out := runCLI(t, dir, "import", "--example"); code != 0 || !strings.HasPrefix(out, "files,caption") {
		t.Errorf("example: %s", out)
	}
}

// Saved templates, hashtag sets and account groups from the command line.
func TestTemplatesHashtagSetsAndGroups(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--files", "a.png", "--template", "weekly tip", "--hashtag-set", "Android",
		"--hashtags", "apps tips", "--yes")
	if code != 0 {
		t.Fatalf("post: %d %s", code, out)
	}
	p := f.posts[0]
	if p["baseCaption"] != "Tip of the week" || p["linkUrl"] != "https://memoshot.app" {
		t.Errorf("template texts: %v", p)
	}
	if got := strings.Join(toStrings(p["baseHashtags"]), " "); got != "apps tips android kotlin" {
		t.Errorf("typed hashtags, then the set's (each once): %s", got)
	}
	if len(p["destinations"].([]any)) != 2 {
		t.Errorf("the template's accounts: %v", p["destinations"])
	}
	code, out = runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "group:launch", "--files", "c.png", "--caption", "x",
		"--tiktok-privacy", "SELF_ONLY", "--yes")
	if code != 0 || len(f.posts[1]["destinations"].([]any)) != 2 {
		t.Errorf("group:launch = Instagram and TikTok: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "group:nope", "--caption", "x"); code == 0 || !strings.Contains(out, "one of: Launch") {
		t.Errorf("unknown group: %s", out)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestAICommandAndPostsList(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	code, out := runCLI(t, dir, "ai", "shorten", "A long caption about the app", "--platform", "x", "--max", "100")
	if code != 0 || !strings.Contains(out, "1. Short one") || !strings.Contains(out, "2 of 20 AI requests") {
		t.Fatalf("ai: %d %s", code, out)
	}
	if c := f.aiCalls[0]; c["action"] != "SHORTEN" || c["platform"] != "X" || c["maxLength"] != float64(100) {
		t.Errorf("request: %v", c)
	}
	if code, _ := runCLI(t, dir, "ai", "dance", "x"); code == 0 {
		t.Error("unknown action")
	}
	runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "Listed")
	if code, out := runCLI(t, dir, "posts", "--json"); code != 0 || !strings.Contains(out, `"baseCaption": "Listed"`) {
		t.Errorf("posts --json: %s", out)
	}
}
