package app

import (
	"flag"
	"fmt"
	"strings"
	"testing"
)

// Every account can get its own caption, title and platform settings from the command line.
func TestPerAccountFlags(t *testing.T) {
	f, dir := setup(t)
	if code, out := runCLI(t, dir, "login", "--code", "mkl_good"); code != 0 {
		t.Fatalf("login: %s", out)
	}
	code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook,instagram,tiktok", "--files", "a.png,b.png",
		"--caption", "Main", "--caption-for", "instagram=Only for Instagram", "--name", "Internal", "--title", "Shown title",
		"--title-for", "tiktok=TikTok photos", "--tiktok-privacy-for", "tiktok=SELF_ONLY", "--tiktok-allow-for", "tiktok=duet",
		"--tiktok-brand", "yours", "--yes")
	if code != 0 {
		t.Fatalf("post: %d %s", code, out)
	}
	body := f.posts[0]
	if body["internalTitle"] != "Internal" {
		t.Errorf("--name is the internal name: %v", body["internalTitle"])
	}
	dests := map[string]map[string]any{}
	for _, d := range body["destinations"].([]any) {
		m := d.(map[string]any)
		dests[m["accountId"].(string)] = m
	}
	if dests["acc-ig"]["caption"] != "Only for Instagram" || dests["acc-fb"]["caption"] != nil {
		t.Errorf("own caption only on Instagram: ig=%v fb=%v", dests["acc-ig"]["caption"], dests["acc-fb"]["caption"])
	}
	tt := dests["acc-tt"]
	opts := tt["options"].(map[string]any)
	if tt["title"] != "TikTok photos" || opts["privacy"] != "SELF_ONLY" || opts["disable_duet"] != nil || opts["disable_comment"] != true ||
		opts["brand_disclose"] != true || opts["brand_organic"] != true {
		t.Errorf("tiktok: %v", tt)
	}

	// A per-account flag for an account that isn't posted to is an error, not silently ignored.
	code, out = runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "x", "--caption-for", "instagram=y")
	if code == 0 || !strings.Contains(out, "--caption-for") {
		t.Errorf("unknown account: %d %s", code, out)
	}
	code, out = runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "tiktok", "--files", "a.png", "--tiktok-privacy", "SELF_ONLY", "--tiktok-brand", "sponsor", "--duplicates", "post")
	if code == 0 || !strings.Contains(out, "commercial content") {
		t.Errorf("bad brand value: %d %s", code, out)
	}
}

func TestListingCommands(t *testing.T) {
	_, dir := setup(t)
	runCLI(t, dir, "login", "--code", "mkl_good")
	if code, out := runCLI(t, dir, "projects"); code != 0 || !strings.Contains(out, "Memoshot") || !strings.Contains(out, "ReelWise") {
		t.Errorf("projects: %s", out)
	}
	code, out := runCLI(t, dir, "accounts", "--project", "memoshot")
	if code != 0 || !strings.Contains(out, "@memoshot.app") || !strings.Contains(out, "reauth required") || !strings.Contains(out, "acc-tt") {
		t.Errorf("accounts: %s", out)
	}
	if code, out := runCLI(t, dir, "accounts", "--project", "nope"); code == 0 || !strings.Contains(out, "one of: Memoshot, ReelWise") {
		t.Errorf("unknown project: %s", out)
	}
	code, out = runCLI(t, dir, "boards", "@qirox", "--json")
	if code != 0 || !strings.Contains(out, `"defaultBoard": "b2"`) || !strings.Contains(out, `"name": "Apps"`) {
		t.Errorf("boards: %s", out)
	}
}

// Saved posts: found by id, by its first characters or as "last"; scheduled, moved, unscheduled, edited, cancelled, deleted.
func TestManagingSavedPosts(t *testing.T) {
	f, dir := setup(t)
	runCLI(t, dir, "login", "--code", "mkl_good")
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "First"); code != 0 {
		t.Fatal(out)
	}
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "Second"); code != 0 {
		t.Fatal(out)
	}
	first, second := postID(1), postID(2)

	if code, out := runCLI(t, dir, "schedule", "last", "--at", "2099-03-01 09:30"); code != 0 || !strings.Contains(out, "scheduled for 2099-03-01 09:30") {
		t.Fatalf("schedule last: %d %s", code, out)
	}
	if f.state[second] != "SCHEDULED" || f.scheduleIn[0]["localDateTime"] != "2099-03-01T09:30:00" {
		t.Errorf("the newest post from this folder is scheduled: %v %v", f.state, f.scheduleIn)
	}
	// Moving it: the server refuses to schedule a scheduled post, so it's unscheduled and scheduled again.
	if code, out := runCLI(t, dir, "reschedule", second, "--next-slot"); code != 0 || !strings.Contains(out, "2099-01-02 09:00") {
		t.Fatalf("reschedule: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "schedule", second, "--at", "2001-01-01 10:00"); code == 0 || !strings.Contains(out, "has passed") {
		t.Errorf("past time: %s", out)
	}
	if code, out := runCLI(t, dir, "delete", second, "--yes"); code == 0 || !strings.Contains(out, "only drafts") {
		t.Errorf("a scheduled post isn't deleted: %s", out)
	}
	if code, out := runCLI(t, dir, "unschedule", second); code != 0 || f.state[second] != "DRAFT" {
		t.Errorf("unschedule: %s", out)
	}

	// The start of an id is enough, as `status` prints it — when it's unambiguous.
	if code, out := runCLI(t, dir, "edit", first[:8], "--caption", "Better", "--hashtags", "a b", "--name", "N"); code != 0 {
		t.Fatalf("edit: %s", out)
	}
	if p := f.patched[0]; p["baseCaption"] != "Better" || fmt.Sprint(p["baseHashtags"]) != "[a b]" || p["internalTitle"] != "N" || p["linkUrl"] != nil {
		t.Errorf("only the given fields change: %v", p)
	}
	if code, out := runCLI(t, dir, "edit", "0000000"); code == 0 || !strings.Contains(out, "matches 2 posts") {
		t.Errorf("ambiguous id: %s", out)
	}
	if code, out := runCLI(t, dir, "edit", first); code == 0 || !strings.Contains(out, "nothing to change") {
		t.Errorf("edit without changes: %s", out)
	}

	// Without a terminal to confirm in, cancelling and deleting need --yes.
	if code, out := runCLI(t, dir, "cancel", first); code == 0 || !strings.Contains(out, "--yes") {
		t.Errorf("cancel without --yes: %s", out)
	}
	f.state[first] = "SCHEDULED"
	if code, out := runCLI(t, dir, "cancel", first, "--yes"); code != 0 || f.state[first] != "CANCELLED" {
		t.Errorf("cancel: %s", out)
	}
	if code, out := runCLI(t, dir, "delete", second, "--yes"); code != 0 || f.state[second] != "DELETED" || !strings.Contains(out, "Draft deleted") {
		t.Errorf("delete: %s", out)
	}
}

// mktauto post -h documents every flag the command takes.
func TestPostHelpListsEveryFlag(t *testing.T) {
	var f postFlags
	newPostFlags(&f).VisitAll(func(fl *flag.Flag) {
		if !strings.Contains(postHelp, "--"+fl.Name+" ") && !strings.Contains(postHelp, "--"+fl.Name+"\n") {
			t.Errorf("post -h doesn't mention --%s", fl.Name)
		}
	})
}

func TestPresets(t *testing.T) {
	f, dir := setup(t)
	runCLI(t, dir, "login", "--code", "mkl_good")
	code, out := runCLI(t, dir, "preset", "save", "launch", "--project", "Memoshot", "--accounts", "facebook,instagram", "--hashtags", "android",
		"--caption-for", "instagram=For IG", "--files", "a.png", "--yes")
	if code != 0 || !strings.Contains(out, "Not saved") || !strings.Contains(out, "--files") {
		t.Fatalf("save: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "preset", "list"); code != 0 || !strings.Contains(out, "launch") || !strings.Contains(out, "--caption-for=instagram=For IG") {
		t.Errorf("list: %s", out)
	}
	// Used from the command line; a flag given here wins over the preset's.
	code, out = runCLI(t, dir, "post", "--preset", "launch", "--files", "b.png", "--caption", "Hi", "--hashtags", "kotlin", "--yes")
	if code != 0 {
		t.Fatalf("post --preset: %d %s", code, out)
	}
	p := f.posts[0]
	if len(p["destinations"].([]any)) != 2 || fmt.Sprint(p["baseHashtags"]) != "[kotlin]" {
		t.Errorf("preset applied, hashtags overridden: %v", p)
	}
	if code, out := runCLI(t, dir, "post", "--preset", "nope", "--files", "b.png"); code == 0 || !strings.Contains(out, "no preset") {
		t.Errorf("unknown preset: %s", out)
	}
	if code, _ := runCLI(t, dir, "preset", "delete", "launch"); code != 0 {
		t.Error("delete")
	}
	if code, out := runCLI(t, dir, "preset", "list"); !strings.Contains(out, "No presets") || code != 0 {
		t.Errorf("after delete: %s", out)
	}
}

func TestExitCodes(t *testing.T) {
	f, dir := setup(t)
	if code, _ := runCLI(t, dir, "status"); code != exitAuth {
		t.Errorf("not signed in: %d", code)
	}
	runCLI(t, dir, "login", "--code", "mkl_good")
	if code, _ := runCLI(t, dir, "post", "--nope"); code != exitUsage {
		t.Errorf("unknown flag: %d", code)
	}
	f.permission = "DRAFT"
	if code, _ := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "x", "--when", "now"); code != exitNotAllowed {
		t.Errorf("not allowed: %d", code)
	}
	f.revoked = true
	if code, _ := runCLI(t, dir, "whoami"); code != exitAuth {
		t.Errorf("signed out: %d", code)
	}
	// --quiet: messages hidden, --json output kept.
	f.revoked = false
	runCLI(t, dir, "login", "--code", "mkl_good")
	if code, out := runCLI(t, dir, "--quiet", "projects"); code != 0 || out != "" {
		t.Errorf("quiet: %d %q", code, out)
	}
	if code, out := runCLI(t, dir, "-q", "projects", "--json"); code != 0 || !strings.Contains(out, `"Memoshot"`) {
		t.Errorf("quiet --json: %d %q", code, out)
	}
}
