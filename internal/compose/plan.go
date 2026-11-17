package compose

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
)

// Item is a file in the post: the local file and, once uploaded (or reused), the stored media.
type Item struct {
	File  media.File
	Media api.Media
}

// When: what happens after the post is saved.
type When struct {
	Kind string // DRAFT, NOW, NEXT_SLOT, AT
	At   string // "YYYY-MM-DD HH:MM" in the project's timezone (AT only)
}

// Plan is everything chosen for one post.
type Plan struct {
	Project  api.Project
	Accounts []api.ProjectAccount // selected
	Items    []Item               // the post's files in their order
	// PerAccount: indexes into Items, in that account's order. Missing = all files in the post's order.
	PerAccount map[string][]int

	InternalTitle string
	Caption       string
	Hashtags      []string
	Link          string
	FirstComment  string
	Captions      map[string]string // accountId → its own caption
	Titles        map[string]string // YouTube / Pinterest / TikTok photo title
	MadeWithAI    bool
	AIOverride    map[string]bool // accountId → its own setting
	TikTok        map[string]TikTokChoice
	YouTubePrivacy map[string]string // "", "unlisted", "private"
	Boards        map[string]string // Pinterest board id ("" = the account's default)
	PinLinks      map[string]string
	CoverIndex    int // index into Items of the cover image for videos; -1 = none
	CoverFrameMs  int // > 0: a frame of the video where an image cover isn't possible
	When          When
}

func NewPlan() *Plan {
	return &Plan{PerAccount: map[string][]int{}, Captions: map[string]string{}, Titles: map[string]string{}, AIOverride: map[string]bool{},
		TikTok: map[string]TikTokChoice{}, YouTubePrivacy: map[string]string{}, Boards: map[string]string{}, PinLinks: map[string]string{}, CoverIndex: -1}
}

// FilesFor is the account's files, in its order (cover image excluded from the post's media).
func (p *Plan) FilesFor(accountID string) []Item {
	if idx, ok := p.PerAccount[accountID]; ok {
		out := make([]Item, 0, len(idx))
		for _, i := range idx {
			if i >= 0 && i < len(p.Items) && i != p.CoverIndex {
				out = append(out, p.Items[i])
			}
		}
		return out
	}
	out := make([]Item, 0, len(p.Items))
	for i, it := range p.Items {
		if i != p.CoverIndex {
			out = append(out, it)
		}
	}
	return out
}

func mediaOf(items []Item) []api.Media {
	out := make([]api.Media, len(items))
	for i, it := range items {
		out[i] = it.Media
	}
	return out
}

// FormatFor is the account's automatic format ("" when it can't post its files).
func (p *Plan) FormatFor(a api.ProjectAccount) string {
	return AutoFormat(a.Platform, mediaOf(p.FilesFor(a.AccountID)))
}

// AIFor: the account's "made with AI" setting (the post's, unless changed for it).
func (p *Plan) AIFor(accountID string) bool {
	if v, ok := p.AIOverride[accountID]; ok {
		return v
	}
	return p.MadeWithAI
}

// Problems the tool can see before saving (the server checks the rest).
func (p *Plan) Problems() []string {
	var out []string
	if len(p.Accounts) == 0 {
		out = append(out, "Choose at least one account")
	}
	for _, a := range p.Accounts {
		name := a.DisplayName + " (" + PlatformLabel[a.Platform] + ")"
		files := mediaOf(p.FilesFor(a.AccountID))
		if AutoFormat(a.Platform, files) == "" {
			out = append(out, name+": "+CantPostReason(a.Platform, files))
		}
		if a.Platform == "YOUTUBE" && strings.TrimSpace(p.titleFor(a.AccountID)) == "" {
			out = append(out, name+": YouTube needs a title")
		}
		if a.Platform == "TIKTOK" {
			if c, ok := p.TikTok[a.AccountID]; ok && c.Privacy == "" && !c.Draft {
				out = append(out, name+": choose who can view it")
			}
		}
	}
	return out
}

func (p *Plan) titleFor(accountID string) string {
	if t, ok := p.Titles[accountID]; ok && strings.TrimSpace(t) != "" {
		return t
	}
	return p.InternalTitle
}

// Input builds the request body: the post's media in order, and per account its format, its own files and order
// (only when they differ from the post's), texts and options.
func (p *Plan) Input() (api.PostInput, error) {
	if probs := p.Problems(); len(probs) > 0 {
		return api.PostInput{}, errors.New(strings.Join(probs, "\n"))
	}
	in := api.PostInput{ProjectID: p.Project.ID, InternalTitle: strings.TrimSpace(p.InternalTitle), BaseCaption: strings.TrimSpace(p.Caption),
		BaseHashtags: p.Hashtags, LinkURL: strings.TrimSpace(p.Link), MediaIDs: []string{}, Destinations: []api.DestinationInput{}}
	if in.BaseHashtags == nil {
		in.BaseHashtags = []string{}
	}
	used := map[int]bool{}
	for _, a := range p.Accounts {
		if idx, ok := p.PerAccount[a.AccountID]; ok {
			for _, i := range idx {
				used[i] = true
			}
		} else {
			for i := range p.Items {
				used[i] = true
			}
		}
	}
	var postOrder []string
	for i, it := range p.Items {
		if used[i] && i != p.CoverIndex {
			postOrder = append(postOrder, it.Media.ID)
		}
	}
	if postOrder != nil {
		in.MediaIDs = postOrder
	}
	var cover *Item
	if p.CoverIndex >= 0 && p.CoverIndex < len(p.Items) {
		cover = &p.Items[p.CoverIndex]
	}
	for _, a := range p.Accounts {
		files := p.FilesFor(a.AccountID)
		format := AutoFormat(a.Platform, mediaOf(files))
		d := api.DestinationInput{AccountID: a.AccountID, Format: format, Options: map[string]any{}}
		ids := make([]string, len(files))
		hasVideo := false
		for i, f := range files {
			ids[i] = f.Media.ID
			hasVideo = hasVideo || f.Media.Kind == "VIDEO"
		}
		if !sameOrder(ids, postOrder) {
			d.MediaIDs = ids
		}
		if c, ok := p.Captions[a.AccountID]; ok && strings.TrimSpace(c) != "" {
			d.Caption = ptr(strings.TrimSpace(c))
		}
		switch a.Platform {
		case "YOUTUBE":
			d.Title = ptr(strings.TrimSpace(p.titleFor(a.AccountID)))
			if v := p.YouTubePrivacy[a.AccountID]; v != "" {
				d.Options["privacy"] = v
			}
		case "PINTEREST":
			if t := strings.TrimSpace(p.titleFor(a.AccountID)); t != "" {
				d.Title = ptr(t)
			}
			if b := p.Boards[a.AccountID]; b != "" {
				d.Options["board_id"] = b
			}
			if l := strings.TrimSpace(p.PinLinks[a.AccountID]); l != "" {
				d.Options["link_url"] = l
			}
		case "TIKTOK":
			c := p.TikTok[a.AccountID]
			for k, v := range c.Options() {
				d.Options[k] = v
			}
			if format == "PHOTO_POST" {
				if t, ok := p.Titles[a.AccountID]; ok && strings.TrimSpace(t) != "" {
					d.Title = ptr(strings.TrimSpace(t))
				}
			}
		}
		if CommentsOn(a.Platform, format) && strings.TrimSpace(p.FirstComment) != "" {
			d.FirstComment = ptr(strings.TrimSpace(p.FirstComment))
		}
		// A TikTok inbox draft carries only the draft flag (the creator sets the rest in TikTok).
		tiktokDraft := a.Platform == "TIKTOK" && p.TikTok[a.AccountID].Draft
		if !tiktokDraft {
			if p.AIFor(a.AccountID) {
				d.Options["ai_generated"] = true
			}
			if cover != nil && CoverImageSupported(a.Platform, format, hasVideo) {
				d.CoverMediaID = cover.Media.ID
			}
			if p.CoverFrameMs > 0 && CoverFrameSupported(a.Platform, format, hasVideo) {
				d.Options["cover_time_ms"] = p.CoverFrameMs
			}
		}
		if len(d.Options) == 0 {
			d.Options = nil
		}
		in.Destinations = append(in.Destinations, d)
	}
	if p.When.Kind == "AT" {
		local, err := LocalDateTime(p.When.At)
		if err != nil {
			return api.PostInput{}, err
		}
		in.Schedule = &api.Schedule{LocalDateTime: local, Timezone: p.Project.Timezone}
	}
	return in, nil
}

func sameOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ptr(s string) *string { return &s }

var dateTime = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})[ T](\d{1,2}):(\d{2})$`)

// LocalDateTime: "2026-11-20 18:00" → "2026-11-20T18:00:00" (wall-clock time in the project's timezone).
func LocalDateTime(s string) (string, error) {
	m := dateTime.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", fmt.Errorf("write the time as YYYY-MM-DD HH:MM, e.g. 2026-11-20 18:00 (got %q)", s)
	}
	t, err := time.Parse("2006-01-02 15:04", fmt.Sprintf("%s-%s-%s %02s:%s", m[1], m[2], m[3], m[4], m[5]))
	if err != nil {
		return "", fmt.Errorf("%q isn't a real date and time", s)
	}
	return t.Format("2006-01-02T15:04:05"), nil
}

// InFuture: the wall-clock time is later than now in that timezone.
func InFuture(s, timezone string, now time.Time) bool {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", s, loc)
	return err == nil && t.After(now)
}

// ParseHashtags: "#a, b c" → ["a", "b", "c"] (the server adds the # and skips tags already in the caption).
func ParseHashtags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		t := strings.TrimLeft(f, "#")
		if t != "" && !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	return out
}

// MatchAccounts picks accounts by id, display name, @handle, label, platform ("instagram", "tiktok") or
// "platform:name"; every term must match at least one account.
func MatchAccounts(terms []string, accounts []api.ProjectAccount) ([]api.ProjectAccount, error) {
	var out []api.ProjectAccount
	picked := map[string]bool{}
	for _, raw := range terms {
		term := strings.ToLower(strings.TrimSpace(raw))
		if term == "" {
			continue
		}
		platform, name, hasPlatform := strings.Cut(term, ":")
		found := false
		for _, a := range accounts {
			ok := false
			switch {
			case term == strings.ToLower(a.AccountID), term == strings.ToLower(a.DisplayName), term == strings.ToLower(a.Label),
				a.Handle != "" && strings.TrimPrefix(term, "@") == strings.ToLower(strings.TrimPrefix(a.Handle, "@")):
				ok = true
			case platformMatches(term, a.Platform):
				ok = true
			case hasPlatform && platformMatches(platform, a.Platform):
				ok = name == strings.ToLower(a.DisplayName) || strings.TrimPrefix(name, "@") == strings.ToLower(strings.TrimPrefix(a.Handle, "@"))
			}
			if ok {
				found = true
				if !picked[a.AccountID] {
					picked[a.AccountID] = true
					out = append(out, a)
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("no account in this project matches %q", raw)
		}
	}
	return out, nil
}

func platformMatches(term, platform string) bool {
	p := strings.ToLower(platform)
	aliases := map[string][]string{
		"facebook_page": {"facebook", "fb", "facebook_page", "page"}, "facebook_group": {"group", "facebook_group"},
		"facebook_profile": {"profile", "facebook_profile"}, "instagram": {"instagram", "ig"}, "threads": {"threads"},
		"x": {"x", "twitter"}, "pinterest": {"pinterest", "pin"}, "tiktok": {"tiktok", "tt"}, "youtube": {"youtube", "yt"},
	}
	for _, a := range aliases[p] {
		if term == a {
			return true
		}
	}
	return false
}
