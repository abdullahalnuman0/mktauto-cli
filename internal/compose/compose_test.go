package compose

import (
	"strings"
	"testing"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
)

func img(id string) api.Media { return api.Media{ID: id, Kind: "IMAGE", Width: 1080, Height: 1350} }
func vid(id string, seconds int, vertical bool) api.Media {
	m := api.Media{ID: id, Kind: "VIDEO", Width: 1920, Height: 1080, DurationMs: seconds * 1000}
	if vertical {
		m.Width, m.Height = 1080, 1920
	}
	return m
}

// Same cases as the website's compose.test.ts: the tool must choose the same formats.
func TestAutoFormatMatchesTheWebsite(t *testing.T) {
	short := []api.Media{vid("v", 30, true)}
	cases := []struct {
		platform string
		media    []api.Media
		want     string
	}{
		{"FACEBOOK_PAGE", short, "REEL"}, {"INSTAGRAM", short, "REEL"}, {"YOUTUBE", short, "SHORT"}, {"TIKTOK", short, "VIDEO"},
		{"PINTEREST", short, "PIN"}, {"FACEBOOK_PAGE", []api.Media{vid("v", 120, true)}, "VIDEO"},
		{"YOUTUBE", []api.Media{vid("v", 200, true)}, "VIDEO"}, {"YOUTUBE", []api.Media{vid("v", 60, false)}, "VIDEO"},
		{"FACEBOOK_PAGE", []api.Media{vid("v", 30, false)}, "VIDEO"},
		{"INSTAGRAM", []api.Media{img("a"), img("b")}, "CAROUSEL"}, {"TIKTOK", []api.Media{img("a")}, "PHOTO_POST"},
		{"X", []api.Media{img("a"), img("b")}, "IMAGE"}, {"YOUTUBE", []api.Media{img("a")}, ""}, {"INSTAGRAM", nil, ""},
		{"THREADS", nil, "TEXT"}, {"TIKTOK", []api.Media{img("a"), vid("v", 10, true)}, ""},
	}
	for _, c := range cases {
		if got := AutoFormat(c.platform, c.media); got != c.want {
			t.Errorf("%s %v: got %q, want %q", c.platform, Kind(c.media), got, c.want)
		}
	}
}

func accounts() []api.ProjectAccount {
	return []api.ProjectAccount{
		{AccountID: "fb", Platform: "FACEBOOK_PAGE", DisplayName: "Memoshot", Status: "ACTIVE"},
		{AccountID: "ig", Platform: "INSTAGRAM", DisplayName: "@memoshot.app", Handle: "memoshot.app", Status: "ACTIVE"},
		{AccountID: "tt", Platform: "TIKTOK", DisplayName: "@memoshot", Status: "ACTIVE"},
		{AccountID: "yt", Platform: "YOUTUBE", DisplayName: "Memoshot TV", Status: "ACTIVE"},
	}
}

func items(ms ...api.Media) []Item {
	out := make([]Item, len(ms))
	for i, m := range ms {
		out[i] = Item{Media: m}
	}
	return out
}

func TestInputGivesEachAccountItsOwnFilesOrderAndOptions(t *testing.T) {
	p := NewPlan()
	p.Project = api.Project{ID: "p1", Timezone: "Asia/Dhaka"}
	acc := accounts()
	p.Accounts = acc[:3]
	p.Items = items(img("m1"), img("m2"), img("m3"), img("m4"), img("m5"))
	p.PerAccount["tt"] = []int{2}          // TikTok: only the 3rd image
	p.PerAccount["ig"] = []int{1, 0, 2, 3} // Instagram: four, its own order
	p.Caption, p.FirstComment, p.MadeWithAI = "  Launch  ", "More: https://x", true
	p.AIOverride["ig"] = false
	p.TikTok["tt"] = TikTokChoice{Privacy: "SELF_ONLY", AllowComments: true}
	p.When = When{Kind: "AT", At: "2026-12-01 18:30"}
	in, err := p.Input()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(in.MediaIDs, ",") != "m1,m2,m3,m4,m5" || in.BaseCaption != "Launch" {
		t.Fatalf("post media/caption: %v %q", in.MediaIDs, in.BaseCaption)
	}
	fb, ig, tt := in.Destinations[0], in.Destinations[1], in.Destinations[2]
	if fb.Format != "CAROUSEL" || fb.MediaIDs != nil || *fb.FirstComment != "More: https://x" || fb.Options["ai_generated"] != true {
		t.Errorf("facebook: %+v", fb)
	}
	if ig.Format != "CAROUSEL" || strings.Join(ig.MediaIDs, ",") != "m2,m1,m3,m4" || ig.Options["ai_generated"] != nil {
		t.Errorf("instagram: %+v %v", ig, ig.Options)
	}
	if tt.Format != "PHOTO_POST" || strings.Join(tt.MediaIDs, ",") != "m3" || tt.FirstComment != nil ||
		tt.Options["privacy"] != "SELF_ONLY" || tt.Options["disable_comment"] != nil || tt.Options["disable_duet"] != true {
		t.Errorf("tiktok: %+v %v", tt, tt.Options)
	}
	if in.Schedule == nil || in.Schedule.LocalDateTime != "2026-12-01T18:30:00" || in.Schedule.Timezone != "Asia/Dhaka" {
		t.Errorf("schedule: %+v", in.Schedule)
	}
}

func TestCoverAndTikTokDraftAndProblems(t *testing.T) {
	p := NewPlan()
	p.Project = api.Project{ID: "p1", Timezone: "UTC"}
	acc := accounts()
	p.Accounts = []api.ProjectAccount{acc[1], acc[2], acc[3]}
	p.Items = items(vid("v1", 20, true), img("cover"))
	p.CoverIndex, p.CoverFrameMs, p.MadeWithAI = 1, 3500, true
	p.TikTok["tt"] = TikTokChoice{Draft: true}
	if _, err := p.Input(); err == nil || !strings.Contains(err.Error(), "YouTube needs a title") {
		t.Fatalf("want the YouTube title problem, got %v", err)
	}
	p.Titles["yt"] = "Tip #1"
	in, err := p.Input()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(in.MediaIDs, ",") != "v1" {
		t.Errorf("the cover isn't post media: %v", in.MediaIDs)
	}
	ig, tt, yt := in.Destinations[0], in.Destinations[1], in.Destinations[2]
	if ig.Format != "REEL" || ig.CoverMediaID != "cover" || ig.Options["cover_time_ms"] != 3500 {
		t.Errorf("instagram reel cover: %+v %v", ig, ig.Options)
	}
	if len(tt.Options) != 1 || tt.Options["draft"] != true || tt.CoverMediaID != "" {
		t.Errorf("a TikTok draft carries only the draft flag: %v", tt.Options)
	}
	if yt.Format != "SHORT" || *yt.Title != "Tip #1" || yt.CoverMediaID != "cover" {
		t.Errorf("youtube: %+v", yt)
	}
	// Instagram can't post text alone.
	p.Items = nil
	p.CoverIndex = -1
	if _, err := p.Input(); err == nil || !strings.Contains(err.Error(), "Instagram needs a photo or video") {
		t.Errorf("want the Instagram problem, got %v", err)
	}
}

func TestTikTokRules(t *testing.T) {
	if p := TikTokProblems(TikTokChoice{}, true, nil, 0, nil); len(p) != 1 {
		t.Errorf("privacy must be chosen: %v", p)
	}
	if p := TikTokProblems(TikTokChoice{Privacy: "PUBLIC_TO_EVERYONE"}, false, nil, 0, nil); len(p) != 1 {
		t.Errorf("unaudited apps post only privately: %v", p)
	}
	if p := TikTokProblems(TikTokChoice{Privacy: "SELF_ONLY", BrandDisclose: true, BrandContent: true}, true, nil, 0, nil); len(p) != 1 {
		t.Errorf("branded content can't be private: %v", p)
	}
	if p := TikTokProblems(TikTokChoice{Privacy: "SELF_ONLY"}, true, []string{"SELF_ONLY"}, 60, []int{61_000}); len(p) != 1 {
		t.Errorf("video too long: %v", p)
	}
	if p := TikTokProblems(TikTokChoice{Draft: true}, false, nil, 0, nil); len(p) != 0 {
		t.Errorf("drafts skip the rules: %v", p)
	}
	if TikTokLabel(TikTokChoice{BrandDisclose: true, BrandOrganic: true}) != "Promotional content" {
		t.Error("label")
	}
}

func TestTimesTagsAndAccountMatching(t *testing.T) {
	if v, err := LocalDateTime("2026-12-01 9:05"); err != nil || v != "2026-12-01T09:05:00" {
		t.Errorf("%q %v", v, err)
	}
	if _, err := LocalDateTime("2026-02-30 10:00"); err == nil {
		t.Error("30 February isn't a date")
	}
	if _, err := LocalDateTime("tomorrow"); err == nil {
		t.Error("needs YYYY-MM-DD HH:MM")
	}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	if !InFuture("2026-11-17T19:00:00", "Asia/Dhaka", now) || InFuture("2026-11-17T17:00:00", "Asia/Dhaka", now) {
		t.Error("Dhaka is UTC+6")
	}
	if got := strings.Join(ParseHashtags("#Android, kotlin  #android\nkmp"), " "); got != "Android kotlin kmp" {
		t.Errorf("hashtags: %q", got)
	}
	acc := accounts()
	m, err := MatchAccounts([]string{"instagram", "@memoshot", "Memoshot TV", "fb"}, acc)
	if err != nil || len(m) != 4 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := MatchAccounts([]string{"pinterest"}, acc); err == nil {
		t.Error("no Pinterest account here")
	}
}

func TestFitAndTextLimits(t *testing.T) {
	img := api.Media{Kind: "IMAGE", Width: 1080, Height: 1350}
	five := []api.Media{img, img, img, img, img}
	if f, why := Fit("X", five); f != "" || why != "X takes up to 4 files" {
		t.Errorf("X with 5 images: %q %q", f, why)
	}
	if f, _ := Fit("INSTAGRAM", five); f != "CAROUSEL" {
		t.Errorf("Instagram with 5 images: %q", f)
	}
	if f, why := Fit("PINTEREST", five[:2]); f != "" || why == "" {
		t.Errorf("Pinterest takes one file: %q %q", f, why)
	}
	if f, why := Fit("TIKTOK", nil); f != "" || why != "TikTok needs a photo or video" {
		t.Errorf("TikTok text: %q %q", f, why)
	}
	if MaxText("X", "TEXT") != 280 || MaxText("INSTAGRAM", "REEL") != 2200 || MaxText("YOUTUBE", "NOPE") != 0 {
		t.Error("limits")
	}
	if n := TextLength("X", "héllo 👋"); n != 7 {
		t.Errorf("code points: %d", n)
	}
	if n := TextLength("THREADS", "hi 👋"); n != 7 {
		t.Errorf("Threads counts emoji bytes: %d", n)
	}
	if n := TextLength("X", "go https://mktauto.site/l/AbC2345 now"); n != 39 {
		t.Errorf("tracked link gains 2: %d", n)
	}
}

func TestVideoLengthKnownBeforeUpload(t *testing.T) {
	vertical := func(sec int) []api.Media {
		return []api.Media{{Kind: "VIDEO", Width: 1080, Height: 1920, DurationMs: sec * 1000}}
	}
	if f, why := Fit("INSTAGRAM", vertical(30)); f != "REEL" || why != "" {
		t.Errorf("30 s reel: %q %q", f, why)
	}
	if f, _ := Fit("FACEBOOK_PAGE", vertical(120)); f != "VIDEO" {
		t.Errorf("longer than a Facebook Reel → a normal video: %q", f)
	}
	if f, _ := Fit("YOUTUBE", vertical(60)); f != "SHORT" {
		t.Errorf("a Short: %q", f)
	}
	if _, why := Fit("TIKTOK", vertical(700)); why != "TikTok takes videos of 0:03 to 10:00 as a Video (this one is 11:40)" {
		t.Errorf("too long for TikTok: %q", why)
	}
	if _, why := Fit("INSTAGRAM", vertical(2)); why == "" {
		t.Error("too short for a Reel")
	}
	if f, why := Fit("TIKTOK", []api.Media{{Kind: "VIDEO"}}); f != "VIDEO" || why != "" {
		t.Errorf("length unknown: the server checks after upload: %q %q", f, why)
	}
}

func TestSplitThread(t *testing.T) {
	long := "Big news today. " + strings.Repeat("Exports are now much faster. ", 12) + "\n\nGet it now: https://reelwise.app"
	parts := SplitThread("X", long, 280, 20)
	if len(parts) < 2 {
		t.Fatalf("split: %v", parts)
	}
	for i, p := range parts {
		limit := 280
		if i == 0 {
			limit -= 20
		}
		if TextLength("X", p) > limit || strings.TrimSpace(p) == "" {
			t.Errorf("part %d is %d chars: %q", i+1, TextLength("X", p), p)
		}
	}
	if !strings.HasPrefix(parts[0], "Big news today.") || !strings.HasSuffix(parts[len(parts)-1], "https://reelwise.app") {
		t.Errorf("sentences kept whole and in order: %q … %q", parts[0], parts[len(parts)-1])
	}
	if got := SplitThread("X", "short", 280, 0); len(got) != 1 || got[0] != "short" {
		t.Errorf("short: %v", got)
	}
	if got := SplitThread("X", strings.Repeat("a", 600), 280, 0); len(got) != 3 || len([]rune(got[0])) != 280 {
		t.Errorf("one long word is cut hard: %v", got)
	}
	if got := SplitParts("one\n---\ntwo\nlines\n --- \n\nthree\n---\n"); len(got) != 3 || got[1] != "two\nlines" {
		t.Errorf("parts: %q", got)
	}
	if JoinParts([]string{"a", "b"}) != "a\n---\nb" {
		t.Error("join")
	}
}
