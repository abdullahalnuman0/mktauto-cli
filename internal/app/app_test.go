package app

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/abdullahalnuman0/mktauto-cli/internal/history"
	"github.com/abdullahalnuman0/mktauto-cli/internal/update"
)

// fakeServer answers like the MKTAuto API (backend CliController, MediaController, PostController) and records calls.
type fakeServer struct {
	t          *testing.T
	srv        *httptest.Server
	mu         sync.Mutex
	permission string
	revoked    bool
	approved   bool
	polls      int
	calls      []string
	redeem     map[string]any
	uploads    []map[string]any
	posts      []map[string]any
	scheduled  []string
	published  []string
	assets     map[string]string // assetId → mime
	usedHashes map[string]string // sha256 → assetId (posted before)
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{t: t, permission: "PUBLISH", assets: map[string]string{}, usedHashes: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeServer) problem(w http.ResponseWriter, status int, code, detail string) {
	f.json(w, status, map[string]any{"status": status, "code": code, "detail": detail})
}

var accountsJSON = []map[string]any{
	{"accountId": "acc-fb", "platform": "FACEBOOK_PAGE", "displayName": "Memoshot", "status": "ACTIVE", "publishMode": "API", "defaultSelected": true, "sortOrder": 1},
	{"accountId": "acc-ig", "platform": "INSTAGRAM", "displayName": "@memoshot.app", "handle": "memoshot.app", "status": "ACTIVE", "publishMode": "API", "sortOrder": 2},
	{"accountId": "acc-tt", "platform": "TIKTOK", "displayName": "@memoshot", "status": "ACTIVE", "publishMode": "API", "sortOrder": 3},
	{"accountId": "acc-pi", "platform": "PINTEREST", "displayName": "Qirox", "status": "REAUTH_REQUIRED", "publishMode": "API", "sortOrder": 4},
}

func (f *fakeServer) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	f.calls = append(f.calls, r.Method+" "+path)
	var body map[string]any
	if r.Body != nil && strings.HasPrefix(path, "/api/") {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
	}
	session := map[string]any{"id": "s1", "name": "studio-pc", "permission": f.permission, "createdVia": "CODE", "expiresAt": "2027-02-15T00:00:00Z"}
	public := path == "/api/cli/redeem" || strings.HasPrefix(path, "/api/cli/device-auth") || strings.HasPrefix(path, "/r2/")
	if !public {
		if r.Header.Get("Authorization") != "Bearer mkc_session" || f.revoked {
			f.problem(w, 401, "invalid_cli_session", "Signed out")
			return
		}
		if r.Header.Get("X-Mktauto-Cli") == "" {
			f.t.Errorf("%s without the version header", path)
		}
	}
	switch {
	case path == "/api/cli/redeem":
		f.redeem = body
		if body["code"] != "mkl_good" {
			f.problem(w, 401, "invalid_login_code", "wrong code")
			return
		}
		f.json(w, 201, map[string]any{"token": "mkc_session", "session": session})
	case path == "/api/cli/device-auth":
		f.json(w, 200, map[string]any{"deviceCode": "mkd_x", "userCode": "BCDF-G7HK", "verificationUrl": "https://web/cli/approve",
			"verificationUrlComplete": "https://web/cli/approve?code=BCDF-G7HK", "expiresIn": 600, "interval": 1})
	case path == "/api/cli/device-auth/poll":
		f.polls++
		if f.polls < 2 {
			f.json(w, 202, map[string]any{"status": "PENDING", "interval": 1})
			return
		}
		f.json(w, 200, map[string]any{"status": "APPROVED", "session": map[string]any{"token": "mkc_session", "session": session}})
	case path == "/api/cli/me":
		f.json(w, 200, map[string]any{"displayName": "Demo", "email": "demo@example.com", "session": session, "webUrl": "https://web"})
	case path == "/api/cli/logout":
		f.revoked = true
		w.WriteHeader(204)
	case path == "/api/projects":
		f.json(w, 200, []map[string]any{{"id": "p1", "name": "Memoshot", "timezone": "Asia/Dhaka"}, {"id": "p2", "name": "ReelWise", "timezone": "UTC"}})
	case path == "/api/projects/p1/accounts":
		f.json(w, 200, accountsJSON)
	case path == "/api/accounts/acc-tt/tiktok-creator-info":
		f.json(w, 200, map[string]any{"nickname": "Memoshot", "username": "memoshot", "privacyLevelOptions": []string{"PUBLIC_TO_EVERYONE", "SELF_ONLY"},
			"maxVideoPostDurationSec": 600, "audited": true})
	case path == "/api/cli/duplicates":
		var out []map[string]any
		for _, h := range body["sha256"].([]any) {
			if asset, ok := f.usedHashes[h.(string)]; ok {
				out = append(out, map[string]any{"sha256": h, "assetId": asset, "storageStatus": "STORED", "uses": []map[string]any{
					{"postId": "old", "postTitle": "Earlier post", "postStatus": "COMPLETED", "accountId": "acc-ig", "accountName": "@memoshot.app",
						"platform": "INSTAGRAM", "status": "PUBLISHED", "publishedAt": "2026-11-12T10:00:00Z"}}})
			}
		}
		f.json(w, 200, out)
	case path == "/api/media/uploads":
		f.uploads = append(f.uploads, body)
		id := fmt.Sprintf("asset-%d", len(f.assets)+1)
		f.assets[id] = body["mimeType"].(string)
		if sha, _ := body["sha256"].(string); sha != "" {
			f.usedHashes[sha] = id
		}
		f.json(w, 201, map[string]any{"assetId": id, "mode": "SINGLE", "url": f.srv.URL + "/r2/" + id})
	case strings.HasPrefix(path, "/r2/"):
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("ETag", `"e"`)
	case strings.HasSuffix(path, "/complete") || (r.Method == "GET" && strings.HasPrefix(path, "/api/media/")):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/media/"), "/complete")
		mime := f.assets[id]
		m := map[string]any{"id": id, "kind": "IMAGE", "mimeType": mime, "width": 1080, "height": 1350, "storageStatus": "STORED"}
		if strings.HasPrefix(mime, "video/") {
			m["kind"], m["width"], m["height"], m["durationMs"] = "VIDEO", 1080, 1920, 20000
		}
		f.json(w, 200, m)
	case path == "/api/posts" && r.Method == "POST":
		f.posts = append(f.posts, body)
		f.json(w, 201, f.postFrom(body, "DRAFT"))
	case strings.HasSuffix(path, "/validate"):
		f.json(w, 200, map[string]any{"valid": true, "errorCount": 0, "warningCount": 0, "destinations": []any{}})
	case strings.HasSuffix(path, "/schedule"):
		f.scheduled = append(f.scheduled, path)
		f.json(w, 200, f.postFrom(f.posts[len(f.posts)-1], "SCHEDULED"))
	case strings.HasSuffix(path, "/publish-now"):
		f.published = append(f.published, path)
		f.json(w, 200, f.postFrom(f.posts[len(f.posts)-1], "SCHEDULED"))
	case strings.HasPrefix(path, "/api/posts/") && r.Method == "GET":
		post := f.postFrom(f.posts[len(f.posts)-1], "COMPLETED")
		for _, d := range post["destinations"].([]map[string]any) {
			d["status"], d["publishedUrl"] = "PUBLISHED", "https://social/p"
		}
		f.json(w, 200, post)
	default:
		f.problem(w, 404, "not_found", "not in the fake server: "+path)
	}
}

func (f *fakeServer) postFrom(body map[string]any, status string) map[string]any {
	var dests []map[string]any
	for i, d := range body["destinations"].([]any) {
		m := d.(map[string]any)
		dests = append(dests, map[string]any{"id": fmt.Sprintf("d%d", i), "accountId": m["accountId"], "platform": "INSTAGRAM", "accountName": m["accountId"],
			"format": m["format"], "status": "DRAFT", "finalText": body["baseCaption"]})
	}
	return map[string]any{"id": fmt.Sprintf("post-%d", len(f.posts)), "projectId": "p1", "status": status, "baseCaption": body["baseCaption"],
		"timezone": "Asia/Dhaka", "destinations": dests}
}

// runCLI runs mktauto in dir with a private settings folder; returns the exit code and what it printed.
func runCLI(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	t.Chdir(dir)
	out, _ := os.CreateTemp(t.TempDir(), "out")
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, out
	code := Run(args, "1.0.0-test")
	os.Stdout, os.Stderr = stdout, stderr
	data, _ := os.ReadFile(out.Name())
	return code, string(data)
}

func setup(t *testing.T) (*fakeServer, string) {
	t.Setenv("MKTAUTO_CONFIG_DIR", t.TempDir())
	t.Setenv("MKTAUTO_TOKEN_STORE", "file")
	t.Setenv("MKTAUTO_NO_TUI", "1")
	t.Setenv("MKTAUTO_NO_BROWSER", "1")
	t.Setenv("MKTAUTO_TOKEN", "")
	t.Setenv("MKTAUTO_NO_UPDATE_CHECK", "1") // tests never ask GitHub
	f := newFakeServer(t)
	t.Setenv("MKTAUTO_SERVER", f.srv.URL)
	dir := t.TempDir()
	for i, name := range []string{"a.png", "b.png", "c.png"} {
		file, _ := os.Create(filepath.Join(dir, name))
		_ = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 10+i, 10)))
		file.Close()
	}
	_ = os.WriteFile(filepath.Join(dir, "reel.mp4"), []byte("video bytes"), 0o600)
	return f, dir
}

func TestLoginWithACodeThenPostWithPerAccountFiles(t *testing.T) {
	f, dir := setup(t)
	if code, out := runCLI(t, dir, "login", "--code", "mkl_wrong"); code == 0 || !strings.Contains(out, "wrong, already used") {
		t.Fatalf("wrong code: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "login", "--code", "mkl_good"); code != 0 || !strings.Contains(out, "Signed in as") {
		t.Fatalf("login: %d %s", code, out)
	}
	dev := f.redeem["device"].(map[string]any)
	if dev["cliVersion"] != "1.0.0-test" || dev["os"] == "" || dev["arch"] == "" {
		t.Errorf("device: %v", dev)
	}

	code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook,instagram,tiktok", "--files", "b.png,a.png,c.png",
		"--only", "tiktok=2", "--only", "instagram=3,1", "--caption", "Launch day", "--hashtags", "android, #kotlin", "--first-comment", "More inside",
		"--ai", "--ai-off-for", "instagram", "--tiktok-privacy", "SELF_ONLY", "--tiktok-allow", "comments", "--when", "2099-01-01 10:00", "--yes")
	if code != 0 {
		t.Fatalf("post: %d %s", code, out)
	}
	if len(f.uploads) != 3 {
		t.Fatalf("uploads: %v", f.uploads)
	}
	for _, u := range f.uploads {
		if len(u["sha256"].(string)) != 64 || u["projectId"] != "p1" {
			t.Errorf("upload request: %v", u)
		}
	}
	// Uploads run two at a time, so asset ids follow upload order: name them by file.
	id := map[string]string{}
	for i, u := range f.uploads {
		id[u["filename"].(string)] = fmt.Sprintf("asset-%d", i+1)
	}
	post := f.posts[0]
	ids := post["mediaIds"].([]any)
	if fmt.Sprint(ids) != fmt.Sprint([]string{id["b.png"], id["a.png"], id["c.png"]}) || post["baseCaption"] != "Launch day" || fmt.Sprint(post["baseHashtags"]) != "[android kotlin]" {
		t.Errorf("post: %v", post)
	}
	if s := post["schedule"].(map[string]any); s["localDateTime"] != "2099-01-01T10:00:00" || s["timezone"] != "Asia/Dhaka" {
		t.Errorf("schedule: %v", s)
	}
	d := post["destinations"].([]any)
	fb, ig, tt := d[0].(map[string]any), d[1].(map[string]any), d[2].(map[string]any)
	if fb["format"] != "CAROUSEL" || fb["mediaIds"] != nil || fb["firstComment"] != "More inside" || fb["options"].(map[string]any)["ai_generated"] != true {
		t.Errorf("facebook: %v", fb)
	}
	if ig["format"] != "CAROUSEL" || fmt.Sprint(ig["mediaIds"]) != fmt.Sprint([]string{id["c.png"], id["b.png"]}) || ig["options"] != nil || ig["firstComment"] != "More inside" {
		t.Errorf("instagram (its own two files in its own order, no AI label): %v", ig)
	}
	opts := tt["options"].(map[string]any)
	if tt["format"] != "PHOTO_POST" || fmt.Sprint(tt["mediaIds"]) != fmt.Sprint([]string{id["a.png"]}) || opts["privacy"] != "SELF_ONLY" ||
		opts["disable_comment"] != nil || opts["disable_duet"] != true || opts["ai_generated"] != true || tt["firstComment"] != nil {
		t.Errorf("tiktok: %v", tt)
	}
	if len(f.scheduled) != 1 || !strings.Contains(out, "Scheduled for 2099-01-01 10:00") || !strings.Contains(out, "Music Usage Confirmation") {
		t.Errorf("schedule call %v, output:\n%s", f.scheduled, out)
	}
	h, _ := history.For("")
	if len(h) != 1 || h[0].Action != "scheduled" || len(h[0].Files) != 3 {
		t.Errorf("history: %+v", h)
	}

	// The same files again: posted before → the tool asks what to do instead of posting them silently.
	code, out = runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "instagram,facebook", "--files", "a.png", "--caption", "Again")
	if code == 0 || !strings.Contains(out, "posted before") || !strings.Contains(out, "Earlier post") {
		t.Fatalf("duplicates must be confirmed: %d %s", code, out)
	}
	code, out = runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "instagram,facebook", "--files", "a.png", "--caption", "Again",
		"--duplicates", "new-only")
	if code != 0 {
		t.Fatalf("new-only: %d %s", code, out)
	}
	// Instagram already had the file: it's left out; Facebook gets it.
	again := f.posts[1]["destinations"].([]any)
	if len(again) != 1 || again[0].(map[string]any)["accountId"] != "acc-fb" || !strings.Contains(out, "already had every file") {
		t.Errorf("new-only: %v\n%s", again, out)
	}
	if len(f.uploads) != 3 {
		t.Errorf("an already stored file is reused, not uploaded again: %d uploads", len(f.uploads))
	}
}

func TestBrowserLoginWaitsForApproval(t *testing.T) {
	f, dir := setup(t)
	code, out := runCLI(t, dir, "login")
	if code != 0 || !strings.Contains(out, "BCDF-G7HK") || !strings.Contains(out, "https://web/cli/approve?code=BCDF-G7HK") || f.polls < 2 {
		t.Fatalf("browser login: %d polls=%d\n%s", code, f.polls, out)
	}
	if code, out := runCLI(t, dir, "whoami"); code != 0 || !strings.Contains(out, "Demo") {
		t.Errorf("whoami: %d %s", code, out)
	}
	if code, _ := runCLI(t, dir, "logout"); code != 0 || !f.revoked {
		t.Error("logout must end the session on the server")
	}
	if code, out := runCLI(t, dir, "whoami"); code == 0 || !strings.Contains(out, "not signed in") {
		t.Errorf("after logout: %d %s", code, out)
	}
}

func TestDryRunSendsNothingAndPermissionsAreRespected(t *testing.T) {
	f, dir := setup(t)
	runCLI(t, dir, "login", "--code", "mkl_good")
	code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "instagram", "--files", "reel.mp4,a.png", "--dry-run", "--caption", "x")
	if code != 0 || !strings.Contains(out, `"format": "CAROUSEL"`) || !strings.Contains(out, "Nothing was uploaded") {
		t.Fatalf("dry run: %d %s", code, out)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "/api/media/uploads") || c == "POST /api/posts" {
			t.Errorf("dry run called %s", c)
		}
	}
	// A reconnect-needed account can't be chosen; a drafts-only terminal can't publish.
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "pinterest", "--files", "a.png"); code == 0 || !strings.Contains(out, "reconnect") {
		t.Errorf("pinterest: %d %s", code, out)
	}
	f.permission = "DRAFT"
	if code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "hi", "--when", "now"); code == 0 || !strings.Contains(out, "may not publish") {
		t.Errorf("drafts only: %d %s", code, out)
	}
}

func TestPublishNowWaitsForTheResultAndARevokedSessionIsForgotten(t *testing.T) {
	f, dir := setup(t)
	runCLI(t, dir, "login", "--code", "mkl_good")
	code, out := runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "Text post", "--when", "now", "--wait", "--json")
	if code != 0 || len(f.published) != 1 || !strings.Contains(out, "published") || !strings.Contains(out, `"status": "COMPLETED"`) {
		t.Fatalf("publish now: %d %v\n%s", code, f.published, out)
	}
	f.revoked = true
	if code, out := runCLI(t, dir, "status"); code == 0 || !strings.Contains(out, "signed out") {
		t.Errorf("revoked: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "whoami"); code == 0 || !strings.Contains(out, "not signed in") {
		t.Errorf("the revoked token is forgotten: %d %s", code, out)
	}
}

func TestUpdateNoticeAndCheck(t *testing.T) {
	_, dir := setup(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/releases/latest" {
			fmt.Fprint(w, `{"tag_name":"v9.0.0"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer gh.Close()
	old := releases
	releases = func() *update.Source { return &update.Source{API: gh.URL + "/api", Download: gh.URL + "/dl", HTTP: gh.Client()} }
	defer func() { releases = old }()
	t.Setenv("MKTAUTO_NO_UPDATE_CHECK", "")
	if code, out := runCLI(t, dir, "history"); code != 0 || !strings.Contains(out, "mktauto v9.0.0 is available") {
		t.Errorf("daily notice: %d %s", code, out)
	}
	if code, out := runCLI(t, dir, "update", "--check"); code != 0 || !strings.Contains(out, "v9.0.0 is available (you have 1.0.0-test)") {
		t.Errorf("update --check: %d %s", code, out)
	}
	// Checked within the day: the saved answer is used (the server isn't asked again).
	gh.Close()
	if code, out := runCLI(t, dir, "history"); code != 0 || !strings.Contains(out, "v9.0.0 is available") {
		t.Errorf("cached notice: %d %s", code, out)
	}
}

