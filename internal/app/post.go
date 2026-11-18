package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/abdullahalnuman0/mktauto-cli/internal/history"
	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
	"github.com/abdullahalnuman0/mktauto-cli/internal/ui"
	"github.com/abdullahalnuman0/mktauto-cli/internal/upload"
	"github.com/charmbracelet/huh"
	"github.com/dustin/go-humanize"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ";") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type postFlags struct {
	project, accounts, files                        string
	only                                            multiFlag
	caption, captionFile, hashtags, link, firstComm string
	title                                           string
	ai                                              bool
	aiOff                                           string
	tiktokPrivacy, tiktokAllow                      string
	tiktokDraft                                     bool
	youtubePrivacy, board                           string
	cover                                           string
	coverFrame                                      float64
	when, duplicates                                string
	dryRun, jsonOut, yes, wait                      bool
	name, pinLink, tiktokBrand, tiktokDraftFor      string
	template, hashtagSet                            string
	repeat, repeatUntil                             string
	preset                                          string
	alt                                             multiFlag // FILE=TEXT
	altAI                                           bool
	threadFor                                       string
	// ACCOUNT=value, repeatable: that account's own value (ADR-076)
	captionFor, captionFileFor, titleFor, youtubePrivacyFor, boardFor, pinLinkFor multiFlag
	tiktokPrivacyFor, tiktokAllowFor, tiktokBrandFor                              multiFlag
}

// perAccountFlags: the flags that take ACCOUNT=value, by name.
func (f *postFlags) perAccountFlags() map[string]*multiFlag {
	return map[string]*multiFlag{"caption-for": &f.captionFor, "caption-file-for": &f.captionFileFor, "title-for": &f.titleFor,
		"youtube-privacy-for": &f.youtubePrivacyFor, "board-for": &f.boardFor, "pin-link-for": &f.pinLinkFor,
		"tiktok-privacy-for": &f.tiktokPrivacyFor, "tiktok-allow-for": &f.tiktokAllowFor, "tiktok-brand-for": &f.tiktokBrandFor}
}

// post: the wizard when run in a terminal without flags, otherwise everything from flags (scripts, CI).
func (e *Env) post(ctx context.Context, args []string) error {
	var f postFlags
	fs := newPostFlags(&f)
	fs.SetOutput(e.Err)
	fs.Usage = func() { fmt.Fprint(e.Err, postHelp) }
	args, err := expandPreset(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	// A preset without files in a terminal: the step-by-step wizard, with the preset's answers filled in.
	wizardWithPreset := e.Interactive && f.preset != "" && f.files == "" && !f.dryRun
	flagMode := !e.Interactive || f.files != "" || (!wizardWithPreset && (f.project != "" || f.accounts != ""))
	if f.jsonOut {
		e.Out = e.Err
	}
	p := &poster{env: e, f: f, wizard: !flagMode, ask: newAsker()}
	return p.run(ctx)
}

func newPostFlags(f *postFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	fs.StringVar(&f.project, "project", "", "project id or name")
	fs.StringVar(&f.accounts, "accounts", "", "comma-separated: account names, @handles, ids or platforms (instagram, tiktok…)")
	fs.StringVar(&f.files, "files", "", "comma-separated file names in posting order (\"all\" = every file); empty = text only")
	fs.Var(&f.only, "only", "ACCOUNT=1,3: that account gets only these files (positions in --files, in this order); repeatable")
	fs.StringVar(&f.caption, "caption", "", "caption")
	fs.StringVar(&f.captionFile, "caption-file", "", "read the caption from a file")
	fs.StringVar(&f.hashtags, "hashtags", "", "hashtags, e.g. \"android,kotlin\"")
	fs.StringVar(&f.link, "link", "", "link (preview on Facebook and Threads, the Pin's link)")
	fs.StringVar(&f.firstComm, "first-comment", "", "first comment on Facebook Pages and Instagram")
	fs.StringVar(&f.title, "title", "", "title (YouTube, Pinterest, TikTok photo posts) and the post's internal name")
	fs.BoolVar(&f.ai, "ai", false, "label it \"made with AI\" on every platform that supports it")
	fs.StringVar(&f.aiOff, "ai-off-for", "", "comma-separated accounts that don't get the AI label")
	fs.StringVar(&f.tiktokPrivacy, "tiktok-privacy", "", "who can view on TikTok: PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, FOLLOWER_OF_CREATOR, SELF_ONLY")
	fs.StringVar(&f.tiktokAllow, "tiktok-allow", "", "comma-separated: comments,duet,stitch (off unless listed)")
	fs.BoolVar(&f.tiktokDraft, "tiktok-draft", false, "send to the TikTok inbox as a draft instead of posting")
	fs.StringVar(&f.youtubePrivacy, "youtube-privacy", "", "public (default), unlisted or private")
	fs.StringVar(&f.board, "board", "", "Pinterest board id (empty = the account's default board)")
	fs.StringVar(&f.cover, "cover", "", "image file in this folder used as the video's cover")
	fs.Float64Var(&f.coverFrame, "cover-frame", 0, "second of the video used as its cover where an image isn't possible")
	fs.StringVar(&f.when, "when", "", "draft (default), now, next-slot, or \"YYYY-MM-DD HH:MM\" in the project's timezone")
	fs.StringVar(&f.duplicates, "duplicates", "", "files that were posted before: post (anyway), new-only (only accounts that haven't had them), skip")
	fs.BoolVar(&f.dryRun, "dry-run", false, "check everything and print the post that would be sent, without uploading or saving")
	fs.BoolVar(&f.jsonOut, "json", false, "print the saved post as JSON (messages go to stderr)")
	fs.BoolVar(&f.yes, "yes", false, "don't ask for confirmation")
	fs.BoolVar(&f.wait, "wait", false, "after publishing now, wait for the result")
	fs.Var(&f.alt, "alt", "FILE=TEXT: an image's alt text (a description for people who can't see it); repeatable")
	fs.BoolVar(&f.altAI, "alt-ai", false, "let the AI write alt text for the images that have none")
	fs.StringVar(&f.threadFor, "thread-for", "", "comma-separated X / Threads accounts whose too-long caption becomes a thread of replies")
	fs.StringVar(&f.preset, "preset", "", "start from a saved preset (mktauto preset list); flags given here win")
	fs.StringVar(&f.repeat, "repeat", "", "post it again every: daily, weekly, biweekly, monthly or N days (with a time)")
	fs.StringVar(&f.repeatUntil, "repeat-until", "", "the last day of the repeats, YYYY-MM-DD (default: until stopped)")
	fs.StringVar(&f.template, "template", "", "start from a template saved on the website (name or id)")
	fs.StringVar(&f.hashtagSet, "hashtag-set", "", "add the hashtags of these saved sets (comma-separated names)")
	fs.StringVar(&f.name, "name", "", "the post's internal name (only you see it; default: --title)")
	fs.StringVar(&f.pinLink, "pin-link", "", "the Pin's link on Pinterest (default: --link)")
	fs.StringVar(&f.tiktokBrand, "tiktok-brand", "", "TikTok commercial content: yours (your brand), partner (branded content) or both: yours,partner")
	fs.StringVar(&f.tiktokDraftFor, "tiktok-draft-for", "", "comma-separated TikTok accounts that get it as an inbox draft")
	perAccountHelp := map[string]string{"caption-for": "its own caption", "caption-file-for": "its own caption from a file",
		"title-for": "its own title (YouTube, Pinterest, TikTok photos)", "youtube-privacy-for": "its own YouTube visibility",
		"board-for": "its own Pinterest board", "pin-link-for": "its own Pin link", "tiktok-privacy-for": "its own TikTok privacy",
		"tiktok-allow-for": "its own TikTok interactions", "tiktok-brand-for": "its own TikTok commercial content"}
	for name, v := range f.perAccountFlags() {
		fs.Var(v, name, "ACCOUNT=value: "+perAccountHelp[name]+" (repeatable)")
	}
	return fs
}

// Content types the wizard offers first; the files and the accounts that can post them follow from it.
const (
	kindText   = "text"
	kindImages = "images"
	kindVideo  = "video"
	kindMixed  = "mixed"
)

type poster struct {
	env    *Env
	f      postFlags
	wizard bool
	ask    *asker
	c      *api.Client
	me     *api.Me
	plan   *compose.Plan

	defaults *media.Defaults
	all      []media.File         // postable files in the folder
	posted   map[string]string    // file name → "posted" / "draft" (this folder's history)
	projAcc  []api.ProjectAccount // the project's accounts
	accFor   string               // project id projAcc belongs to

	// Each step's own choices; kept when the user goes back so the questions show them again.
	kind         string
	picked       []media.File         // chosen files, in the post's order
	listDraft    []media.File         // ticked in the file list (in order), before the order screen
	chosen       []api.ProjectAccount // ticked accounts
	files        []media.File         // picked minus files left out as duplicates
	dups         map[string]api.Duplicate
	dupChoice    map[string]string // sha256 → post / new-only / skip
	cover        *media.File       // cover image (not posted as media)
	coverChoice  string            // none / frame / image
	coverSecond  string
	coverImage   string               // path
	uploaded     map[string]api.Media // path → stored media (not uploaded twice)
	textsSet     bool
	captionFrom  string // file whose sidecar caption filled the caption
	hashtags     string
	ownCaption   map[string]string // accountId → its own caption (kept even when unticked)
	captionMode  string            // same / own
	captionIDs   []string          // accounts with their own caption
	captionHow   map[string]string // accountId → keep / edit / empty / copy
	whenChoice   string
	repeatChoice string
	repeatUntil  string
	whenAt       string
	tiktok       map[string]*api.TikTokCreatorInfo
	aiOffIDs     []string                     // accounts the AI label was turned off for
	forv         map[string]map[string]string // per-account flags: flag → accountId → value

	presetsLoaded bool
	templates     []api.Template
	sets          []api.HashtagSet
	groups        []api.AccountGroup
	template      *api.Template // chosen
	applied       *api.Template // whose values are in the texts
	appliedID     string
	setIDs        []string // hashtag sets ticked in the wizard

	ctx       context.Context
	aiChecked bool
	aiStatus  *api.AIStatus
	wantAI    bool      // "AI caption help" ticked on the texts screen
	result    *api.Post // what was saved (batch reports it)
	altMode   string    // none / write / ai
}

func (p *poster) run(ctx context.Context) error {
	e := p.env
	p.ctx = ctx
	var err error
	if p.c, err = e.client(); err != nil {
		return err
	}
	if p.me, err = e.me(ctx, p.c); err != nil {
		return err
	}
	if p.defaults, err = media.LoadDefaults(e.Dir); err != nil {
		return fmt.Errorf("mktauto.yml: %w", err)
	}
	if p.all, err = media.Scan(e.Dir); err != nil {
		return err
	}
	media.ProbeAll(p.all) // video length and shape before the upload, when ffprobe is installed
	p.plan = compose.NewPlan()
	p.tiktok = map[string]*api.TikTokCreatorInfo{}
	p.dups, p.dupChoice, p.uploaded, p.ownCaption = map[string]api.Duplicate{}, map[string]string{}, map[string]api.Media{}, map[string]string{}
	p.captionHow = map[string]string{}
	p.posted = postedHere(e.Dir)

	steps := []step{
		{"project", func() error { return p.chooseProject(ctx) }},
		{"", func() error { return p.loadAccounts(ctx) }},
		{"template", func() error { return p.chooseTemplate(ctx) }},
		{"content", p.chooseKind},
		{"files", p.chooseFiles},
		{"accounts", p.chooseAccounts},
		{"", p.parseForFlags},
		{"", func() error { return p.checkDuplicates(ctx) }},
		{"cover", p.chooseCover},
		{"", func() error { return p.uploadFiles(ctx) }},
		{"per account", p.choosePerAccount},
		{"alt text", p.altTextStep},
		{"texts", p.details},
		{"ai caption", func() error { return p.aiHelp(ctx) }},
		{"captions", p.captionsPerAccount},
		{"ai", p.aiPerAccount},
		{"platforms", func() error { return p.platformSettings(ctx) }},
		{"when", func() error { return p.chooseWhen(ctx) }},
		{"review", p.confirm},
	}
	if err := runSteps(p.ask, steps, false); err != nil {
		return err
	}
	return p.saveAndAct(ctx)
}

// postedHere: files posted (or saved as drafts) from this folder before, by name.
func postedHere(dir string) map[string]string {
	out := map[string]string{}
	entries, _ := history.For(dir)
	for _, en := range entries {
		for _, f := range en.Files {
			if en.Action != "draft" {
				out[f.Name] = "posted"
			} else if out[f.Name] == "" {
				out[f.Name] = "draft"
			}
		}
	}
	return out
}

// ------------------------------------------------------------------ project, content, files, accounts

func (p *poster) chooseProject(ctx context.Context) error {
	projects, err := p.c.Projects(ctx)
	if err != nil {
		return err
	}
	var live []api.Project
	for _, pr := range projects {
		if !pr.Archived {
			live = append(live, pr)
		}
	}
	if len(live) == 0 {
		return errors.New("no projects available to this terminal: create one on the website")
	}
	want := firstNonEmpty(p.f.project, p.defaults.Project)
	if want == "" && !p.wizard && len(live) == 1 {
		want = live[0].ID
	}
	find := func(term string) *api.Project {
		for i, pr := range live {
			if strings.EqualFold(pr.ID, term) || strings.EqualFold(pr.Name, term) {
				return &live[i]
			}
		}
		return nil
	}
	if !p.wizard {
		pr := find(want)
		if pr == nil {
			return fmt.Errorf("choose a project with --project (one of: %s)", names(live))
		}
		p.plan.Project = *pr
		return nil
	}
	chosen := ""
	switch {
	case p.plan.Project.ID != "" && find(p.plan.Project.ID) != nil: // came back to this question
		chosen = p.plan.Project.ID
	case find(want) != nil:
		chosen = find(want).ID
	case find(p.env.Config.LastProject[p.env.Dir]) != nil:
		chosen = p.env.Config.LastProject[p.env.Dir]
	}
	if len(live) == 1 {
		if p.plan.Project.ID == "" {
			p.env.printf("Project: %s\n", bold.Render(live[0].Name))
		}
		p.plan.Project = live[0]
		return nil
	}
	opts := make([]huh.Option[string], len(live))
	for i, pr := range live {
		opts[i] = huh.NewOption(pr.Name, pr.ID)
	}
	if err := p.ask.form(huh.NewSelect[string]().Title("Project").Options(opts...).Value(&chosen)); err != nil {
		return err
	}
	p.plan.Project = *find(chosen)
	return nil
}

func (p *poster) loadAccounts(ctx context.Context) error {
	if p.accFor == p.plan.Project.ID {
		return nil
	}
	accounts, err := p.c.ProjectAccounts(ctx, p.plan.Project.ID)
	if err != nil {
		return err
	}
	slices.SortFunc(accounts, func(a, b api.ProjectAccount) int { return a.SortOrder - b.SortOrder })
	if len(accounts) == 0 {
		return errors.New("this project has no accounts: link some on the website (Projects)")
	}
	p.projAcc, p.accFor, p.chosen = accounts, p.plan.Project.ID, nil
	return nil
}

func names(ps []api.Project) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return strings.Join(out, ", ")
}

func accountLabel(a api.ProjectAccount) string {
	s := compose.PlatformLabel[a.Platform] + " · " + a.DisplayName
	if a.Label != "" {
		s += " (" + a.Label + ")"
	}
	if a.PublishMode == "MANUAL" {
		s += " · you post it"
	}
	return s
}

func countKinds(files []media.File) (images, videos int) {
	for _, f := range files {
		if f.IsVideo() {
			videos++
		} else {
			images++
		}
	}
	return
}

// chooseKind: what is being posted. It narrows the file list and preselects the accounts that can post it.
func (p *poster) chooseKind() error {
	if !p.wizard {
		return nil
	}
	images, videos := countKinds(p.all)
	if images+videos == 0 {
		if p.kind == "" {
			p.env.printf("%s\n", faint.Render("No images or videos in this folder: this will be a text post."))
		}
		p.kind = kindText
		return nil
	}
	var opts []huh.Option[string]
	if images > 0 {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Images · one photo or a carousel  (%d here)", images), kindImages))
	}
	if videos > 0 {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Video · Reel, Short, TikTok, YouTube  (%d here)", videos), kindVideo))
	}
	if images > 0 && videos > 0 || videos > 1 {
		opts = append(opts, huh.NewOption("Images and videos together · a mixed carousel", kindMixed))
	}
	opts = append(opts, huh.NewOption("Text only · no files", kindText))
	if p.kind == "" {
		p.kind = opts[0].Value
	}
	desc := fmt.Sprintf("This folder: %s, %s", plural(images, "image"), plural(videos, "video"))
	if n := len(p.posted); n > 0 {
		desc += fmt.Sprintf(" · %d used before from here", min(n, len(p.all)))
	}
	return p.ask.form(huh.NewSelect[string]().Title("What are you posting?").Description(desc).Options(opts...).Value(&p.kind))
}

func (p *poster) fileLabel(f media.File) string {
	s := f.Name + "  " + faint.Render(humanize.Bytes(uint64(f.Size)))
	if f.DurationMs > 0 {
		s += faint.Render(" · " + compose.Clock(f.DurationMs))
	}
	if f.Width > 0 {
		s += faint.Render(fmt.Sprintf(" · %d×%d", f.Width, f.Height))
	}
	switch p.posted[f.Name] {
	case "posted":
		s += yellow.Render(" · posted before")
	case "draft":
		s += faint.Render(" · in a draft")
	}
	return s
}

func (p *poster) chooseFiles() error {
	if !p.wizard {
		p.picked = nil
		spec := strings.TrimSpace(p.f.files)
		switch spec {
		case "":
			return nil // text only
		case "all":
			p.picked = slices.Clone(p.all)
			return nil
		}
		for _, name := range splitList(spec) {
			f, ok := media.Describe(filePath(p.env.Dir, name))
			if !ok {
				return fmt.Errorf("%s: not found, or not an image/video MKTAuto accepts (JPEG, PNG, WebP, GIF, MP4, MOV, WebM)", name)
			}
			p.picked = append(p.picked, f)
		}
		media.ProbeAll(p.picked)
		return nil
	}
	var pool []media.File
	for _, f := range p.all {
		if p.kind == kindMixed || (p.kind == kindVideo) == f.IsVideo() {
			pool = append(pool, f)
		}
	}
	// What was picked before (when coming back) and is still in this list.
	var prev []media.File
	for _, f := range p.picked {
		if slices.ContainsFunc(pool, func(x media.File) bool { return x.Path == f.Path }) {
			prev = append(prev, f)
		}
	}
	switch {
	case p.kind == kindText:
		p.picked = nil
		return nil
	case p.kind == kindVideo:
		if len(pool) == 1 {
			p.picked = pool
			p.env.printf("Video: %s\n", bold.Render(pool[0].Name))
			return nil
		}
		pick := 0
		if len(prev) > 0 {
			pick = slices.IndexFunc(pool, func(x media.File) bool { return x.Path == prev[0].Path })
		} else if i := slices.IndexFunc(pool, func(x media.File) bool { return p.posted[x.Name] != "posted" }); i >= 0 {
			pick = i
		}
		opts := make([]huh.Option[int], len(pool))
		for i, f := range pool {
			opts[i] = huh.NewOption(p.fileLabel(f), i)
		}
		if err := p.ask.form(huh.NewSelect[int]().Title("Which video").Description("one video per post · run mktauto again for the next one").
			Options(opts...).Value(&pick).Height(min(len(pool)+2, 16))); err != nil {
			return err
		}
		p.picked = []media.File{pool[pick]}
		return nil
	}
	// Two questions: which files, then their order (Esc on the order goes back to the list).
	minFiles, title := 1, "Images"
	if p.kind == kindMixed {
		minFiles, title = 2, "Images and videos"
	}
	if p.listDraft == nil {
		p.listDraft = prev
	}
	return runSteps(p.ask, []step{
		{run: func() error {
			var ids []int
			opts := make([]huh.Option[int], len(pool))
			for i, f := range pool {
				sel := slices.ContainsFunc(p.listDraft, func(x media.File) bool { return x.Path == f.Path })
				if len(p.listDraft) == 0 && p.picked == nil {
					sel = len(pool) <= 10 && p.posted[f.Name] != "posted" // first time: every new file, in a small folder
				}
				if sel {
					ids = append(ids, i)
				}
				opts[i] = huh.NewOption(p.fileLabel(f), i).Selected(sel)
			}
			if err := p.ask.form(huh.NewMultiSelect[int]().Title(title).Description("space to tick · enter to continue · the order comes next").
				Options(opts...).Value(&ids).Limit(35).Height(min(len(pool)+4, 18)).Validate(func(v []int) error {
				if len(v) < minFiles {
					return fmt.Errorf("tick at least %s", plural(minFiles, "file"))
				}
				return nil
			})); err != nil {
				return err
			}
			// Keep the earlier order for files that stay ticked; new ones follow in folder order.
			var chosen []media.File
			for _, f := range p.listDraft {
				if slices.ContainsFunc(ids, func(i int) bool { return pool[i].Path == f.Path }) {
					chosen = append(chosen, f)
				}
			}
			for _, i := range ids {
				if !slices.ContainsFunc(chosen, func(x media.File) bool { return x.Path == pool[i].Path }) {
					chosen = append(chosen, pool[i])
				}
			}
			p.listDraft = chosen
			return nil
		}},
		{run: func() error {
			chosen := p.listDraft
			order := allIndexes(len(chosen))
			if len(chosen) > 1 {
				labels := make([]string, len(chosen))
				for i, f := range chosen {
					labels[i] = f.Name
				}
				if err := p.ask.screen(func() (err error) {
					order, err = ui.Order("Order: the first file shows first", labels)
					return err
				}); err != nil {
					return err
				}
			}
			p.picked = make([]media.File, len(order))
			for i, x := range order {
				p.picked[i] = chosen[x]
			}
			p.listDraft = slices.Clone(p.picked)
			return nil
		}},
	}, false)
}

// localMedia describes files as the server will (video length is only known after upload).
func localMedia(files []media.File) []api.Media {
	out := make([]api.Media, len(files))
	for i, f := range files {
		out[i] = api.Media{Kind: f.Kind, Width: f.Width, Height: f.Height, SizeBytes: f.Size, DurationMs: f.DurationMs}
	}
	return out
}

func (p *poster) chooseAccounts() error {
	accounts := p.projAcc
	if !p.wizard {
		terms := splitList(p.f.accounts)
		if len(terms) == 0 && p.template != nil {
			terms = p.template.AccountIDs
		}
		if len(terms) == 0 {
			terms = p.defaults.Accounts
		}
		terms, err := p.expandGroups(terms)
		if err != nil {
			return err
		}
		if len(terms) == 0 {
			return errors.New("choose accounts with --accounts (names, @handles, ids or platforms)")
		}
		chosen, err := compose.MatchAccounts(terms, accounts)
		if err != nil {
			return err
		}
		for _, a := range chosen {
			if !a.Usable() {
				return fmt.Errorf("%s can't post right now (%s): reconnect or enable it on the website", accountLabel(a), strings.ToLower(a.Status))
			}
		}
		p.chosen = chosen
		return nil
	}
	files := localMedia(p.picked)
	hasVideo := slices.ContainsFunc(p.picked, func(f media.File) bool { return f.IsVideo() })
	var fit []api.ProjectAccount
	var notes []string
	for _, a := range accounts {
		if !a.Usable() {
			notes = append(notes, accountLabel(a)+": "+strings.ToLower(strings.ReplaceAll(a.Status, "_", " ")))
			continue
		}
		if _, why := compose.Fit(a.Platform, files); why != "" {
			notes = append(notes, accountLabel(a)+": "+lowerFirst(strings.TrimPrefix(why, compose.PlatformLabel[a.Platform]+" ")))
			continue
		}
		fit = append(fit, a)
	}
	if len(fit) == 0 {
		p.env.printf("%s None of this project's accounts can post %s:\n", yellow.Render("!"), p.kindText())
		for _, n := range notes {
			p.env.printf("    %s\n", n)
		}
		return errBack
	}
	// Ticked: what was ticked before, else mktauto.yml's accounts, else the project's default ones, else all that fit.
	pre := map[string]bool{}
	for _, a := range p.chosen {
		pre[a.AccountID] = true
	}
	if len(pre) == 0 && p.f.accounts != "" { // from a preset
		if terms, err := p.expandGroups(splitList(p.f.accounts)); err == nil {
			if m, err := compose.MatchAccounts(terms, fit); err == nil {
				for _, a := range m {
					pre[a.AccountID] = true
				}
			}
		}
	}
	if len(pre) == 0 && p.template != nil {
		for _, id := range p.template.AccountIDs {
			pre[id] = true
		}
	}
	if len(pre) == 0 && len(p.defaults.Accounts) > 0 {
		if m, err := compose.MatchAccounts(p.defaults.Accounts, fit); err == nil {
			for _, a := range m {
				pre[a.AccountID] = true
			}
		}
	}
	if len(pre) == 0 {
		for _, a := range fit {
			pre[a.AccountID] = a.DefaultSelected
		}
	}
	if !slices.ContainsFunc(fit, func(a api.ProjectAccount) bool { return pre[a.AccountID] }) {
		for _, a := range fit {
			pre[a.AccountID] = true
		}
	}
	var opts []huh.Option[string]
	// Account groups first: ticking one ticks its accounts (those that can post this).
	for _, g := range p.groups {
		n := 0
		for _, a := range fit {
			if slices.Contains(g.AccountIDs, a.AccountID) {
				n++
			}
		}
		if n > 0 {
			opts = append(opts, huh.NewOption(brand.Render("◆ ")+g.Name+faint.Render(fmt.Sprintf(" · group, %s", plural(n, "account"))), "group:"+g.ID))
		}
	}
	for _, a := range fit {
		label := accountLabel(a)
		if f, _ := compose.Fit(a.Platform, files); f != "" && (!hasVideo || videosMeasured(p.picked)) {
			label += faint.Render(" → " + compose.FormatLabel[f])
		}
		opts = append(opts, huh.NewOption(label, a.AccountID).Selected(pre[a.AccountID]))
	}
	desc := "accounts that can post " + p.kindText() + " · space to tick, enter to continue"
	if len(notes) > 0 {
		desc += "\n" + faint.Render("Not here: "+strings.Join(notes, " · "))
	}
	var ids []string
	if err := p.ask.form(huh.NewMultiSelect[string]().Title("Accounts").Description(desc).Options(opts...).Value(&ids).
		Height(min(len(opts)+3+strings.Count(desc, "\n"), 22)).
		Validate(func(v []string) error {
			if len(v) == 0 {
				return errors.New("tick at least one account")
			}
			return nil
		})); err != nil {
		return err
	}
	ids, err := p.expandGroups(ids) // a ticked group ticks its accounts
	if err != nil {
		return err
	}
	p.chosen = nil
	for _, a := range fit {
		if slices.Contains(ids, a.AccountID) {
			p.chosen = append(p.chosen, a)
		}
	}
	return nil
}

// videosMeasured: every video's length and shape are known (so its format is too).
func videosMeasured(files []media.File) bool {
	for _, f := range files {
		if f.IsVideo() && f.DurationMs == 0 {
			return false
		}
	}
	return true
}

func (p *poster) kindText() string {
	images, videos := countKinds(p.picked)
	switch {
	case images+videos == 0:
		return "a text post"
	case videos == 0 && images == 1:
		return "one image"
	case videos == 0:
		return fmt.Sprintf("%d images together", images)
	case images == 0 && videos == 1:
		return "a video"
	}
	return "these images and videos together"
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// ------------------------------------------------------------------ already posted?

func (p *poster) checkDuplicates(ctx context.Context) error {
	p.files = nil
	p.plan.Accounts = slices.Clone(p.chosen)
	p.plan.PerAccount = map[string][]int{}
	if len(p.picked) == 0 {
		return nil
	}
	var todo []media.File
	for _, f := range p.picked {
		if f.SHA256 == "" {
			todo = append(todo, f)
		}
	}
	if len(todo) > 0 {
		p.env.printf("%s\n", faint.Render(fmt.Sprintf("Checking %s…", plural(len(todo), "file"))))
		if err := media.Fingerprint(todo, nil); err != nil {
			return err
		}
		var hashes []string
		for _, f := range todo {
			for i := range p.picked {
				if p.picked[i].Path == f.Path {
					p.picked[i].SHA256 = f.SHA256
				}
			}
			hashes = append(hashes, f.SHA256)
		}
		found, err := p.c.Duplicates(ctx, hashes)
		if err != nil {
			return err
		}
		for _, d := range found {
			p.dups[d.SHA256] = d
		}
	}
	var questions []step
	for _, f := range p.picked {
		uses := usedSomewhere(p.dups[f.SHA256].Uses)
		if len(uses) == 0 {
			continue
		}
		if !p.wizard {
			p.env.printf("%s %s was used before:\n", yellow.Render("!"), bold.Render(f.Name))
			for _, l := range usesLines(uses) {
				p.env.printf("    %s\n", l)
			}
			if _, err := p.duplicateChoice(f); err != nil {
				return err
			}
			continue
		}
		var had []string
		for _, a := range p.plan.Accounts {
			if slices.ContainsFunc(uses, func(u api.Use) bool { return u.AccountID == a.AccountID }) {
				had = append(had, a.AccountID)
			}
		}
		questions = append(questions, step{run: func() error { return p.duplicateQuestion(f, uses, had) }})
	}
	// Ask (each can go back to the one before), then apply the answers.
	if err := runSteps(p.ask, questions, false); err != nil {
		return err
	}
	var keep []media.File
	exclude := map[int][]string{} // index in keep → accounts that already had it
	for _, f := range p.picked {
		uses := usedSomewhere(p.dups[f.SHA256].Uses)
		if len(uses) == 0 {
			keep = append(keep, f)
			continue
		}
		var had []string
		for _, a := range p.plan.Accounts {
			if slices.ContainsFunc(uses, func(u api.Use) bool { return u.AccountID == a.AccountID }) {
				had = append(had, a.AccountID)
			}
		}
		choice, _ := p.duplicateChoice(f)
		switch {
		case choice == "skip":
		case choice == "new-only" && len(had) == len(p.plan.Accounts):
			p.env.printf("  %s\n", faint.Render(f.Name+": every chosen account already had it, left out"))
		case choice == "new-only":
			exclude[len(keep)] = had
			keep = append(keep, f)
		default:
			keep = append(keep, f)
		}
	}
	p.files = keep
	for i := range p.files {
		for _, acc := range exclude[i] {
			if _, ok := p.plan.PerAccount[acc]; !ok {
				p.plan.PerAccount[acc] = allIndexes(len(p.files))
			}
		}
	}
	for i, accs := range exclude {
		for _, acc := range accs {
			p.plan.PerAccount[acc] = slices.DeleteFunc(p.plan.PerAccount[acc], func(x int) bool { return x == i })
		}
	}
	if len(p.files) == 0 {
		p.env.printf("%s\n", yellow.Render("! Every file was left out."))
		if p.wizard {
			return errBack
		}
		return errors.New("every file was left out: nothing to post")
	}
	// An account that already had every file is left out of this post (rather than posting it without them).
	var keepAccounts []api.ProjectAccount
	for _, a := range p.plan.Accounts {
		if idx, ok := p.plan.PerAccount[a.AccountID]; ok && len(idx) == 0 {
			p.env.printf("  %s\n", faint.Render(accountLabel(a)+": already had every file, left out of this post"))
			delete(p.plan.PerAccount, a.AccountID)
			continue
		}
		keepAccounts = append(keepAccounts, a)
	}
	if len(keepAccounts) == 0 {
		if p.wizard {
			p.env.printf("%s\n", yellow.Render("! Every chosen account already had these files."))
			return errBack
		}
		return errors.New("every chosen account already had these files: nothing to post")
	}
	p.plan.Accounts = keepAccounts
	return nil
}

func usedSomewhere(uses []api.Use) []api.Use {
	var out []api.Use
	for _, u := range uses {
		if u.Status != "CANCELLED" && u.Status != "SKIPPED" && u.PostStatus != "CANCELLED" {
			out = append(out, u)
		}
	}
	return out
}

func allIndexes(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// duplicateChoice: the answer for a file used before (flag mode: --duplicates).
func (p *poster) duplicateChoice(f media.File) (string, error) {
	if p.wizard {
		return p.dupChoice[f.SHA256], nil
	}
	switch p.f.duplicates {
	case "post", "new-only", "skip":
		return p.f.duplicates, nil
	}
	return "", fmt.Errorf("%s was posted before: add --duplicates=post (anyway), new-only (only accounts that haven't had it) or skip", f.Name)
}

// usesLines: where a file was used before (at most 5 lines).
func usesLines(uses []api.Use) []string {
	var lines []string
	for i, u := range uses {
		if i == 5 {
			lines = append(lines, fmt.Sprintf("… and %d more", len(uses)-5))
			break
		}
		when := strings.ToLower(strings.ReplaceAll(u.Status, "_", " "))
		switch {
		case u.PublishedAt != nil:
			when = "posted " + u.PublishedAt.Local().Format("2 Jan 2006")
		case u.ScheduledAt != nil:
			when = strings.ToLower(u.Status) + " for " + u.ScheduledAt.Local().Format("2 Jan 2006 15:04")
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %s  %s", compose.PlatformLabel[u.Platform], u.AccountName, when, truncate(u.PostTitle, 40)))
	}
	return lines
}

func (p *poster) duplicateQuestion(f media.File, uses []api.Use, had []string) error {
	lines := usesLines(uses)
	var opts []huh.Option[string]
	def := "skip"
	if len(had) > 0 && len(had) < len(p.plan.Accounts) {
		n := len(p.plan.Accounts) - len(had)
		label := fmt.Sprintf("Only to the %d accounts that haven't had it", n)
		if n == 1 {
			label = "Only to the account that hasn't had it"
		}
		opts = append(opts, huh.NewOption(label, "new-only"))
		def = "new-only"
	}
	opts = append(opts, huh.NewOption("Leave this file out", "skip"), huh.NewOption("Post it again to every chosen account", "post"))
	choice := p.dupChoice[f.SHA256]
	if !slices.ContainsFunc(opts, func(o huh.Option[string]) bool { return o.Value == choice }) {
		choice = def
	}
	if err := p.ask.form(huh.NewSelect[string]().Title(f.Name + " was used before").Description(strings.Join(lines, "\n")).
		Options(opts...).Value(&choice)); err != nil {
		return err
	}
	p.dupChoice[f.SHA256] = choice
	return nil
}

// ------------------------------------------------------------------ cover

func (p *poster) chooseCover() error {
	p.cover, p.plan.CoverFrameMs = nil, 0
	hasVideo := slices.ContainsFunc(p.files, func(f media.File) bool { return f.IsVideo() })
	if !hasVideo {
		return nil
	}
	if !p.wizard {
		if p.f.cover != "" {
			f, ok := media.Describe(filepath.Join(p.env.Dir, p.f.cover))
			if !ok || f.IsVideo() {
				return fmt.Errorf("--cover %s: must be an image in this folder", p.f.cover)
			}
			p.cover = &f
		}
		if p.f.coverFrame > 0 {
			p.plan.CoverFrameMs = int(p.f.coverFrame * 1000)
		}
		return nil
	}
	var images []media.File
	for _, f := range p.all {
		if !f.IsVideo() && !slices.ContainsFunc(p.files, func(x media.File) bool { return x.Path == f.Path }) {
			images = append(images, f)
		}
	}
	hasTikTok := slices.ContainsFunc(p.plan.Accounts, func(a api.ProjectAccount) bool { return a.Platform == "TIKTOK" })
	imageIdx := max(0, slices.IndexFunc(images, func(f media.File) bool { return f.Path == p.coverImage }))
	return runSteps(p.ask, []step{
		{run: func() error {
			if p.coverChoice == "" {
				p.coverChoice = "none"
			}
			opts := []huh.Option[string]{huh.NewOption("Platform default", "none"), huh.NewOption("A second of the video", "frame")}
			if len(images) > 0 {
				opts = append(opts, huh.NewOption("An image from this folder (Facebook, Instagram Reels, YouTube, Pinterest)", "image"))
			} else if p.coverChoice == "image" {
				p.coverChoice = "none"
			}
			return p.ask.form(huh.NewSelect[string]().Title("Video cover").Options(opts...).Value(&p.coverChoice))
		}},
		{run: func() error {
			if p.coverChoice != "image" {
				return nil
			}
			opts := make([]huh.Option[int], len(images))
			for i, f := range images {
				opts[i] = huh.NewOption(p.fileLabel(f), i)
			}
			if err := p.ask.form(huh.NewSelect[int]().Title("Cover image").Options(opts...).Value(&imageIdx)); err != nil {
				return err
			}
			p.coverImage = images[imageIdx].Path
			return nil
		}},
		{run: func() error {
			if p.coverChoice == "none" || (p.coverChoice == "image" && !hasTikTok) {
				return nil
			}
			optional := p.coverChoice == "image"
			desc := "e.g. 3 or 4.5"
			if optional {
				desc = "TikTok can only use a frame · empty = TikTok's default"
			}
			return p.ask.form(huh.NewInput().Title("Second of the video for the cover").Description(desc).Value(&p.coverSecond).
				Validate(func(v string) error {
					if strings.TrimSpace(v) == "" && optional {
						return nil
					}
					if x, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err != nil || x < 0 {
						return errors.New("enter seconds, e.g. 3 or 4.5")
					}
					return nil
				}))
		}},
		{run: func() error {
			if p.coverChoice == "image" {
				c := images[imageIdx]
				p.cover = &c
			}
			if p.coverChoice != "none" {
				if x, err := strconv.ParseFloat(strings.TrimSpace(p.coverSecond), 64); err == nil {
					p.plan.CoverFrameMs = int(x * 1000)
				}
			}
			return nil
		}},
	}, false)
}

// ------------------------------------------------------------------ upload

func (p *poster) uploadFiles(ctx context.Context) error {
	all := slices.Clone(p.files)
	if p.cover != nil {
		if p.cover.SHA256 == "" {
			p.cover.SHA256, _ = media.SHA256(p.cover.Path)
		}
		all = append(all, *p.cover)
	}
	items := make([]compose.Item, len(all))
	var todo []int
	for i, f := range all {
		if m, ok := p.uploaded[f.Path]; ok {
			items[i] = compose.Item{File: f, Media: m}
		} else if p.f.dryRun {
			// Nothing leaves this computer: use what's known locally (video length is measured by the server after upload).
			items[i] = compose.Item{File: f, Media: api.Media{ID: "(" + f.Name + ")", Kind: f.Kind, Width: f.Width, Height: f.Height, SizeBytes: f.Size}}
		} else {
			todo = append(todo, i)
		}
	}
	if len(todo) > 0 {
		names := make([]string, len(todo))
		sizes := make([]int64, len(todo))
		for j, i := range todo {
			names[j], sizes[j] = all[i].Name, all[i].Size
		}
		prog := ui.StartProgress(names, sizes, p.env.Interactive, func(s string) { p.env.printf("%s\n", s) })
		up := upload.New(p.c)
		var wg sync.WaitGroup
		var mu sync.Mutex
		errs := make([]error, len(todo))
		sem := make(chan struct{}, 2)
		for j, i := range todo {
			wg.Add(1)
			go func(j, i int, f media.File) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				// Already stored (same fingerprint): reuse it instead of uploading again.
				if d, ok := p.dups[f.SHA256]; ok && d.StorageStatus == "STORED" && d.AssetID != "" {
					if m, err := p.c.Media(ctx, d.AssetID); err == nil && m.StorageStatus == "STORED" {
						items[i] = compose.Item{File: f, Media: *m}
						mu.Lock()
						p.uploaded[f.Path] = *m
						mu.Unlock()
						prog.Finish(j, "reused")
						return
					}
				}
				m, err := up.Upload(ctx, f, p.plan.Project.ID, func(sent, _ int64) { prog.Update(j, sent) })
				if err != nil {
					errs[j] = err
					prog.Finish(j, "failed")
					return
				}
				items[i] = compose.Item{File: f, Media: *m}
				mu.Lock()
				p.uploaded[f.Path] = *m
				mu.Unlock()
				prog.Finish(j, "done")
			}(j, i, all[i])
		}
		wg.Wait()
		prog.Done()
		if err := errors.Join(errs...); err != nil {
			return err
		}
	}
	p.plan.CoverIndex = -1
	if p.cover != nil {
		p.plan.CoverIndex = len(items) - 1
	}
	p.plan.Items = items
	return nil
}

// ------------------------------------------------------------------ which files go where

func (p *poster) choosePerAccount() error {
	n := len(p.files)
	if !p.wizard {
		for _, spec := range p.f.only {
			acc, list, ok := strings.Cut(spec, "=")
			if !ok {
				return fmt.Errorf("--only %q: write ACCOUNT=1,3", spec)
			}
			matched, err := compose.MatchAccounts([]string{acc}, p.plan.Accounts)
			if err != nil {
				return fmt.Errorf("--only: %w (it must be one of the chosen accounts)", err)
			}
			var idx []int
			for _, s := range splitList(list) {
				i, err := strconv.Atoi(s)
				if err != nil || i < 1 || i > n {
					return fmt.Errorf("--only %q: positions are 1 to %d", spec, n)
				}
				idx = append(idx, i-1)
			}
			for _, a := range matched {
				p.plan.PerAccount[a.AccountID] = idx
			}
		}
		return nil
	}
	if n < 2 {
		return nil
	}
	accs := p.plan.Accounts
	rows := make([]string, n)
	for i, f := range p.files {
		rows[i] = f.Name
	}
	cols := make([]ui.Column, len(accs))
	sel := make([][]bool, n)
	for r := range sel {
		sel[r] = make([]bool, len(accs))
	}
	own := map[int][]int{} // column → its own order (file indexes)
	for c, a := range accs {
		cols[c] = ui.Column{Short: platformShort(a.Platform) + " " + a.DisplayName}
		idx, ok := p.plan.PerAccount[a.AccountID]
		for r := range rows {
			sel[r][c] = !ok || slices.Contains(idx, r)
		}
		if ok && !slices.IsSorted(idx) {
			own[c] = slices.Clone(idx)
		}
	}
	verdict := func(c int, ticked []int) ui.Verdict {
		order := orderFor(own[c], ticked)
		ms := make([]api.Media, len(order))
		for i, r := range order {
			ms[i] = p.plan.Items[r].Media
		}
		f, _ := compose.Fit(accs[c].Platform, ms)
		if f == "" {
			return ui.Verdict{Label: "can't post", OK: false}
		}
		return ui.Verdict{Label: fmt.Sprintf("%s (%d)", compose.FormatLabel[f], len(order)), OK: true}
	}
	for first := true; ; first = false {
		col := -1
		show := func() (err error) {
			col, err = ui.Grid(rows, cols, sel, verdict)
			return err
		}
		var err error
		if first {
			err = p.ask.screen(show) // one question, however often the grid comes back after an order screen
		} else {
			err = uiErr(show())
		}
		if err != nil {
			return err
		}
		if col < 0 {
			break
		}
		var ticked []int
		for r := range rows {
			if sel[r][col] {
				ticked = append(ticked, r)
			}
		}
		current := orderFor(own[col], ticked)
		labels := make([]string, len(current))
		for i, r := range current {
			labels[i] = rows[r]
		}
		order, err := ui.Order("Order for "+cols[col].Short, labels)
		if errors.Is(err, ui.ErrCancelled) {
			return huh.ErrUserAborted
		}
		if err != nil && !errors.Is(err, ui.ErrBack) {
			return err
		}
		if err == nil {
			o := make([]int, len(order))
			for i, x := range order {
				o[i] = current[x]
			}
			own[col] = o
		}
	}
	for c, a := range accs {
		var ticked []int
		for r := range rows {
			if sel[r][c] {
				ticked = append(ticked, r)
			}
		}
		order := orderFor(own[c], ticked)
		if len(order) == n && own[c] == nil {
			delete(p.plan.PerAccount, a.AccountID) // all files, the post's order
		} else {
			p.plan.PerAccount[a.AccountID] = order
		}
	}
	return nil
}

// orderFor: the ticked files in the column's own order (when set), then any newly ticked ones in post order.
func orderFor(own, ticked []int) []int {
	if own == nil {
		return ticked
	}
	var out []int
	for _, r := range own {
		if slices.Contains(ticked, r) {
			out = append(out, r)
		}
	}
	for _, r := range ticked {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func platformShort(p string) string {
	return map[string]string{"FACEBOOK_PAGE": "FB", "FACEBOOK_GROUP": "FB group", "FACEBOOK_PROFILE": "FB profile", "INSTAGRAM": "IG",
		"THREADS": "Threads", "X": "X", "PINTEREST": "Pin", "TIKTOK": "TikTok", "YOUTUBE": "YT"}[p]
}

// ------------------------------------------------------------------ texts and per-platform settings

// finalLength: about how long the text gets on the platform (caption, then the hashtags).
func (p *poster) finalLength(platform, caption string) int {
	return compose.TextLength(platform, compose.FinalText(caption, p.plan.Hashtags))
}

// tooLong: accounts whose text would be over the platform's limit, with a line saying so.
func (p *poster) tooLong(caption func(a api.ProjectAccount) string) map[string]string {
	out := map[string]string{}
	for _, a := range p.plan.Accounts {
		limit := compose.MaxText(a.Platform, p.plan.FormatFor(a))
		if n := p.finalLength(a.Platform, caption(a)); limit > 0 && n > limit {
			out[a.AccountID] = fmt.Sprintf("%s allows %s characters; this text has about %s", accountLabel(a), humanize.Comma(int64(limit)), humanize.Comma(int64(n)))
		}
	}
	return out
}

// limitsLine: the text limits of the chosen accounts, shortest first ("X 280 · Threads 500 · Instagram 2,200").
func (p *poster) limitsLine() string {
	seen := map[string]int{}
	for _, a := range p.plan.Accounts {
		if l := compose.MaxText(a.Platform, p.plan.FormatFor(a)); l > 0 && l < 10000 {
			seen[compose.PlatformLabel[a.Platform]] = l
		}
	}
	type pl struct {
		name  string
		limit int
	}
	var list []pl
	for n, l := range seen {
		list = append(list, pl{n, l})
	}
	slices.SortFunc(list, func(a, b pl) int { return a.limit - b.limit })
	var parts []string
	for _, x := range list {
		parts = append(parts, x.name+" "+humanize.Comma(int64(x.limit)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Limits: " + strings.Join(parts, " · ")
}

func (p *poster) initTexts() error {
	plan := p.plan
	first := ""
	if len(p.files) > 0 {
		first = p.files[0].Path
	}
	if p.textsSet {
		// The caption came from the first file's .txt and the first file changed: take the new one's.
		if p.captionFrom != "" && p.captionFrom != first && plan.Caption == media.SidecarCaption(media.File{Path: p.captionFrom, Name: filepath.Base(p.captionFrom)}) {
			plan.Caption, p.captionFrom = "", ""
			if first != "" {
				if c := media.SidecarCaption(p.files[0]); c != "" {
					plan.Caption, p.captionFrom = c, first
				}
			}
		}
		p.applyTemplate()
		return nil
	}
	p.textsSet = true
	caption := p.f.caption
	if p.f.captionFile != "" {
		data, err := os.ReadFile(p.f.captionFile)
		if err != nil {
			return err
		}
		caption = strings.TrimSpace(string(data))
	}
	caption = firstNonEmpty(caption, p.defaults.Caption)
	if caption == "" && first != "" {
		if caption = media.SidecarCaption(p.files[0]); caption != "" {
			p.captionFrom = first
		}
	}
	p.hashtags = firstNonEmpty(p.f.hashtags, strings.Join(p.defaults.Hashtags, " "))
	plan.Caption = caption
	plan.Link = firstNonEmpty(p.f.link, p.defaults.Link)
	plan.FirstComment = firstNonEmpty(p.f.firstComm, p.defaults.FirstComment)
	plan.MadeWithAI = p.f.ai || (p.defaults.MadeWithAI != nil && *p.defaults.MadeWithAI)
	plan.InternalTitle = firstNonEmpty(p.f.name, p.f.title)
	p.applyTemplate()
	return nil
}

func (p *poster) details() error {
	if err := p.initTexts(); err != nil {
		return err
	}
	plan := p.plan
	if p.wizard {
		needsComment := slices.ContainsFunc(plan.Accounts, func(a api.ProjectAccount) bool { return compose.CommentsOn(a.Platform, plan.FormatFor(a)) })
		limits := p.limitsLine()
		count := func() string {
			s := humanize.Comma(int64(len([]rune(plan.Caption)))) + " characters"
			if limits != "" {
				s += " · " + limits
			}
			return s + " · ctrl+e opens your editor"
		}
		fields := []huh.Field{
			huh.NewInput().Title("Internal name (only you see it)").Placeholder("optional").Value(&plan.InternalTitle),
			huh.NewText().Title("Caption").DescriptionFunc(count, &plan.Caption).Value(&plan.Caption).CharLimit(63000).Lines(5).ExternalEditor(true),
			huh.NewInput().Title("Hashtags").Placeholder("android kotlin #productivity").Value(&p.hashtags),
			huh.NewInput().Title("Link").Placeholder("https://… (optional)").Value(&plan.Link),
		}
		if needsComment {
			fields = append(fields, huh.NewInput().Title("First comment").Description("Facebook Pages and Instagram, right after the post goes live").Value(&plan.FirstComment))
		}
		if len(p.sets) > 0 {
			var setOpts []huh.Option[string]
			for _, s := range p.sets {
				setOpts = append(setOpts, huh.NewOption(s.Name+faint.Render("  #"+strings.Join(s.Tags, " #")), s.ID).Selected(slices.Contains(p.setIDs, s.ID)))
			}
			fields = append(fields, huh.NewMultiSelect[string]().Title("Hashtag sets").Description("their hashtags are added to the ones above").
				Options(setOpts...).Value(&p.setIDs).Height(min(len(setOpts)+1, 8)))
		}
		if p.aiReady(p.ctx) {
			fields = append(fields, huh.NewConfirm().Title("AI caption help next?").
				Description("improve, shorten, change the tone, translate or suggest hashtags · "+p.aiUsage()).Value(&p.wantAI))
		}
		fields = append(fields, huh.NewConfirm().Title("Made with AI?").Description("Adds each platform's own AI label (none exists on Facebook)").Value(&plan.MadeWithAI))
		if err := p.ask.form(fields...); err != nil {
			return err
		}
	}
	tags, err := p.hashtagsWithSets()
	if err != nil {
		return err
	}
	plan.Hashtags = tags
	return nil
}

// captionsPerAccount: the same caption everywhere, or an account's own. An own caption starts empty, from a copy
// of the main one, or keeps what was written for it before.
func (p *poster) captionsPerAccount() error {
	plan := p.plan
	if !p.wizard {
		clear(plan.Captions)
		for _, a := range plan.Accounts {
			if v, ok := p.forValue("caption-file-for", a); ok {
				data, err := os.ReadFile(v)
				if err != nil {
					return err
				}
				plan.Captions[a.AccountID] = strings.TrimSpace(string(data))
			}
			if v, ok := p.forValue("caption-for", a); ok {
				plan.Captions[a.AccountID] = v
			}
		}
		// Threads (X, Threads): an own caption with "---" lines, or --thread-for to split a too-long one.
		clear(plan.Threads)
		threadFor := mustMatch(splitList(p.f.threadFor), plan.Accounts)
		for _, a := range plan.Accounts {
			text, own := plan.Captions[a.AccountID]
			if !own {
				text = plan.Caption
			}
			if main, parts := threadOf(a.Platform, text); len(parts) > 0 {
				plan.Captions[a.AccountID], plan.Threads[a.AccountID] = main, parts
				continue
			}
			if slices.ContainsFunc(threadFor, func(x api.ProjectAccount) bool { return x.AccountID == a.AccountID }) {
				if !compose.ThreadPlatform(a.Platform) {
					return fmt.Errorf("--thread-for: %s isn't X or Threads", accountLabel(a))
				}
				parts := p.splitFor(a, text)
				if len(parts) > 1 {
					plan.Captions[a.AccountID], plan.Threads[a.AccountID] = parts[0], parts[1:]
				}
			}
		}
		over := p.tooLong(func(a api.ProjectAccount) string {
			if c, ok := plan.Captions[a.AccountID]; ok {
				return c
			}
			return plan.Caption
		})
		for _, a := range plan.Accounts {
			if l, ok := over[a.AccountID]; ok {
				hint := "give it its own with --caption-for"
				if compose.ThreadPlatform(a.Platform) {
					hint += ", or make it a thread with --thread-for " + platformShortName(a.Platform)
				}
				p.env.printf("%s %s (%s)\n", yellow.Render("!"), l, hint)
			}
		}
		return nil
	}
	over := p.tooLong(func(api.ProjectAccount) string { return plan.Caption })
	if len(plan.Accounts) < 2 && len(over) == 0 {
		clear(plan.Captions)
		return nil
	}
	if p.captionMode == "" {
		p.captionMode = "same"
		if len(over) > 0 {
			p.captionMode = "own"
		}
	}
	return runSteps(p.ask, []step{
		{run: func() error {
			desc := ""
			if len(over) > 0 {
				var lines []string
				for _, a := range plan.Accounts {
					if l, ok := over[a.AccountID]; ok {
						lines = append(lines, yellow.Render("! ")+l)
					}
				}
				desc = strings.Join(lines, "\n")
			}
			return p.ask.form(huh.NewSelect[string]().Title("Caption on each account").Description(desc).Options(
				huh.NewOption("The same caption everywhere", "same"),
				huh.NewOption("A different caption for some accounts", "own")).Value(&p.captionMode))
		}},
		{run: func() error {
			if p.captionMode == "same" {
				clear(plan.Captions)
				clear(plan.Threads)
				return nil
			}
			var ids []string
			opts := make([]huh.Option[string], len(plan.Accounts))
			for i, a := range plan.Accounts {
				_, had := plan.Captions[a.AccountID]
				_, long := over[a.AccountID]
				label := accountLabel(a)
				if long {
					label += yellow.Render(" · too long")
				}
				sel := had || long || slices.Contains(p.captionIDs, a.AccountID)
				if sel {
					ids = append(ids, a.AccountID)
				}
				opts[i] = huh.NewOption(label, a.AccountID).Selected(sel)
			}
			if err := p.ask.form(huh.NewMultiSelect[string]().Title("Which accounts get their own caption").Options(opts...).Value(&ids).
				Validate(func(v []string) error {
					if len(v) == 0 {
						return errors.New("tick at least one (or go back and choose the same caption everywhere)")
					}
					return nil
				})); err != nil {
				return err
			}
			p.captionIDs = ids
			return nil
		}},
		{run: func() error {
			if p.captionMode == "same" {
				return nil
			}
			ids := p.captionIDs
			var steps []step
			for _, a := range plan.Accounts {
				if slices.Contains(ids, a.AccountID) {
					steps = append(steps, p.ownCaptionSteps(a)...)
				}
			}
			if err := runSteps(p.ask, steps, false); err != nil {
				return err
			}
			for id := range plan.Captions {
				if !slices.Contains(ids, id) {
					delete(plan.Captions, id)
					delete(plan.Threads, id)
				}
			}
			return nil
		}},
	}, false)
}

func (p *poster) ownCaptionSteps(a api.ProjectAccount) []step {
	plan := p.plan
	limit := compose.MaxText(a.Platform, plan.FormatFor(a))
	fresh := false
	return []step{
		{run: func() error {
			_, had := p.ownCaption[a.AccountID]
			var opts []huh.Option[string]
			if had {
				opts = append(opts, huh.NewOption("Keep what I wrote for it", "keep"), huh.NewOption("Edit it", "edit"))
			}
			opts = append(opts, huh.NewOption("Start with an empty box", "empty"), huh.NewOption("Edit a copy of the main caption", "copy"))
			if compose.ThreadPlatform(a.Platform) && strings.TrimSpace(plan.Caption) != "" {
				opts = append(opts, huh.NewOption("Split the main caption into a thread of replies", "thread"))
			}
			if limit > 0 && limit < 10000 && strings.TrimSpace(plan.Caption) != "" && p.aiReady(p.ctx) {
				opts = append(opts, huh.NewOption(fmt.Sprintf("Let AI shorten the main caption to fit %s", compose.PlatformLabel[a.Platform]), "ai"))
			}
			start := p.captionHow[a.AccountID]
			if !slices.ContainsFunc(opts, func(o huh.Option[string]) bool { return o.Value == start }) {
				start = opts[0].Value
			}
			desc := ""
			if limit > 0 && limit < 10000 {
				desc = fmt.Sprintf("%s takes up to %s characters (hashtags included)", compose.PlatformLabel[a.Platform], humanize.Comma(int64(limit)))
			}
			passing := p.ask.replaying()
			if err := p.ask.form(huh.NewSelect[string]().Title("Caption for " + accountLabel(a)).Description(desc).Options(opts...).Value(&start)); err != nil {
				return err
			}
			p.captionHow[a.AccountID] = start
			fresh = !passing // just answered: the box starts as chosen; otherwise it shows what was written
			return nil
		}},
		{run: func() error {
			text := ""
			start := p.captionHow[a.AccountID]
			if _, written := p.ownCaption[a.AccountID]; !fresh && written {
				start = "edit" // came back to this box: show the text as it was written
			}
			fresh = false
			switch start {
			case "keep":
				plan.Captions[a.AccountID] = p.ownCaption[a.AccountID]
				return nil
			case "edit":
				text = p.ownCaption[a.AccountID]
			case "copy":
				text = plan.Caption
			case "thread":
				text = compose.JoinParts(p.splitFor(a, plan.Caption))
			case "ai":
				short, err := p.aiShortenFor(p.ctx, a, limit)
				if err != nil {
					p.env.printf("%s %v\n", red.Render("✗"), err)
				}
				text = firstNonEmpty(short, plan.Caption)
			}
			count := func() string {
				n := p.finalLength(a.Platform, text)
				s := humanize.Comma(int64(n)) + " characters with the hashtags"
				if limit > 0 && limit < 10000 {
					s += " · up to " + humanize.Comma(int64(limit))
				}
				if compose.ThreadPlatform(a.Platform) {
					if _, parts := threadOf(a.Platform, text); len(parts) > 0 {
						s = fmt.Sprintf("a thread: the post and %s", plural(len(parts), "reply"))
					}
					s += " · a line with --- starts the next reply"
				}
				return s + " · ctrl+e opens your editor"
			}
			if err := p.ask.form(huh.NewText().Title("Caption for "+accountLabel(a)).DescriptionFunc(count, &text).Value(&text).
				Lines(8).CharLimit(63000).ExternalEditor(true).Validate(func(s string) error {
				main, parts := threadOf(a.Platform, s)
				if n := p.finalLength(a.Platform, main); limit > 0 && n > limit {
					return fmt.Errorf("%s allows %s characters (now about %s)", compose.PlatformLabel[a.Platform], humanize.Comma(int64(limit)), humanize.Comma(int64(n)))
				}
				if len(parts) > compose.ThreadMaxParts {
					return fmt.Errorf("a thread has at most %d replies", compose.ThreadMaxParts)
				}
				for i, part := range parts {
					if n := compose.TextLength(a.Platform, part); limit > 0 && n > limit {
						return fmt.Errorf("reply %d is %d characters; %s allows %d", i+1, n, compose.PlatformLabel[a.Platform], limit)
					}
				}
				return nil
			})); err != nil {
				return err
			}
			p.ownCaption[a.AccountID] = text
			main, parts := threadOf(a.Platform, text)
			plan.Captions[a.AccountID] = main
			if len(parts) > 0 {
				plan.Threads[a.AccountID] = parts
			} else {
				delete(plan.Threads, a.AccountID)
			}
			return nil
		}},
	}
}

// aiPerAccount: with "made with AI" on, which accounts get the label.
func (p *poster) aiPerAccount() error {
	plan := p.plan
	clear(plan.AIOverride)
	for _, a := range mustMatch(splitList(p.f.aiOff), plan.Accounts) {
		plan.AIOverride[a.AccountID] = false
	}
	if !p.wizard || !plan.MadeWithAI {
		return nil
	}
	var opts []huh.Option[string]
	for _, a := range plan.Accounts {
		if compose.AILabelSupported(a.Platform, false) {
			opts = append(opts, huh.NewOption(accountLabel(a), a.AccountID).Selected(!p.aiOff()[a.AccountID]))
		}
	}
	if len(opts) < 2 {
		return nil
	}
	var on []string
	if err := p.ask.form(huh.NewMultiSelect[string]().Title("AI label on").Options(opts...).Value(&on)); err != nil {
		return err
	}
	p.aiOffIDs = nil
	for _, o := range opts {
		if !slices.Contains(on, o.Value) {
			plan.AIOverride[o.Value] = false
			p.aiOffIDs = append(p.aiOffIDs, o.Value)
		}
	}
	return nil
}

func (p *poster) aiOff() map[string]bool {
	out := map[string]bool{}
	for _, id := range p.aiOffIDs {
		out[id] = true
	}
	return out
}

func (p *poster) platformSettings(ctx context.Context) error {
	var steps []step
	for _, a := range p.plan.Accounts {
		switch a.Platform {
		case "YOUTUBE":
			steps = append(steps, step{run: func() error { return p.youtube(a) }})
		case "PINTEREST":
			steps = append(steps, step{run: func() error { return p.pinterest(ctx, a) }})
		case "TIKTOK":
			steps = append(steps, step{run: func() error { return p.tiktokSettings(ctx, a) }})
		}
	}
	return runSteps(p.ask, steps, false)
}

func (p *poster) youtube(a api.ProjectAccount) error {
	title, ok := p.plan.Titles[a.AccountID]
	if !ok {
		own, _ := p.forValue("title-for", a)
		title = firstNonEmpty(own, p.f.title, p.plan.InternalTitle, firstLine(p.plan.Caption))
	}
	if len([]rune(title)) > 100 {
		title = string([]rune(title)[:100])
	}
	privacy, ok := p.plan.YouTubePrivacy[a.AccountID]
	if !ok {
		own, _ := p.forValue("youtube-privacy-for", a)
		privacy = strings.ToLower(firstNonEmpty(own, p.f.youtubePrivacy))
		if !slices.Contains([]string{"", "public", "unlisted", "private"}, privacy) {
			return fmt.Errorf("YouTube visibility %q: use public, unlisted or private", privacy)
		}
	}
	if privacy == "public" {
		privacy = ""
	}
	if p.wizard {
		if err := p.ask.form(
			huh.NewInput().Title("YouTube title · "+a.DisplayName).CharLimit(100).Value(&title).Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("YouTube needs a title")
				}
				return nil
			}),
			huh.NewSelect[string]().Title("Visibility").Options(huh.NewOption("Public", ""), huh.NewOption("Unlisted", "unlisted"), huh.NewOption("Private", "private")).Value(&privacy),
		); err != nil {
			return err
		}
	}
	p.plan.Titles[a.AccountID] = title
	p.plan.YouTubePrivacy[a.AccountID] = privacy
	return nil
}

func (p *poster) pinterest(ctx context.Context, a api.ProjectAccount) error {
	board, ok := p.plan.Boards[a.AccountID]
	if !ok {
		own, _ := p.forValue("board-for", a)
		board = firstNonEmpty(own, p.f.board)
	}
	title, ok := p.plan.Titles[a.AccountID]
	if !ok {
		own, _ := p.forValue("title-for", a)
		title = firstNonEmpty(own, p.f.title, p.plan.InternalTitle)
	}
	pinLink, ok := p.plan.PinLinks[a.AccountID]
	if !ok {
		own, _ := p.forValue("pin-link-for", a)
		pinLink = firstNonEmpty(own, p.f.pinLink)
	}
	if p.wizard {
		accounts, err := p.c.Accounts(ctx)
		if err != nil {
			return err
		}
		var boards []api.Board
		def := ""
		for _, acc := range accounts {
			if acc.ID == a.AccountID {
				boards, def = acc.Boards()
			}
		}
		fields := []huh.Field{huh.NewInput().Title("Pin title · " + a.DisplayName).CharLimit(100).Value(&title)}
		if len(boards) > 0 {
			opts := []huh.Option[string]{huh.NewOption("Default board", "")}
			for _, b := range boards {
				label := b.Name
				if b.ID == def {
					label += " (default)"
				}
				opts = append(opts, huh.NewOption(label, b.ID))
			}
			fields = append(fields, huh.NewSelect[string]().Title("Board").Options(opts...).Value(&board))
		}
		fields = append(fields, huh.NewInput().Title("Pin link").Placeholder(firstNonEmpty(p.plan.Link, "empty = the post's link")).Value(&pinLink))
		if err := p.ask.form(fields...); err != nil {
			return err
		}
	}
	p.plan.Titles[a.AccountID] = title
	p.plan.Boards[a.AccountID] = board
	p.plan.PinLinks[a.AccountID] = pinLink
	return nil
}

// tiktokSettings follows TikTok's rules for posting apps: the creator's nickname and current options are shown,
// nothing is preselected for privacy, interactions are off unless turned on, and commercial content is declared.
func (p *poster) tiktokSettings(ctx context.Context, a api.ProjectAccount) error {
	info := p.tiktok[a.AccountID]
	if info == nil {
		var err error
		if info, err = p.c.TikTokCreatorInfo(ctx, a.AccountID); err != nil {
			return fmt.Errorf("TikTok %s: %w", a.DisplayName, err)
		}
		p.tiktok[a.AccountID] = info
	}
	c, ok := p.plan.TikTok[a.AccountID]
	if !ok {
		privacy, _ := p.forValue("tiktok-privacy-for", a)
		c = compose.TikTokChoice{Privacy: strings.ToUpper(firstNonEmpty(privacy, p.f.tiktokPrivacy)), Draft: p.f.tiktokDraft}
		if len(mustMatch(splitList(p.f.tiktokDraftFor), []api.ProjectAccount{a})) > 0 {
			c.Draft = true
		}
		allow, ok := p.forValue("tiktok-allow-for", a)
		if !ok {
			allow = p.f.tiktokAllow
		}
		for _, x := range splitList(allow) {
			switch strings.ToLower(x) {
			case "comments", "comment":
				c.AllowComments = true
			case "duet":
				c.AllowDuet = true
			case "stitch":
				c.AllowStitch = true
			case "none", "-":
			default:
				return fmt.Errorf("TikTok allow %q: use comments, duet, stitch or none", x)
			}
		}
		brand, ok := p.forValue("tiktok-brand-for", a)
		if !ok {
			brand = p.f.tiktokBrand
		}
		for _, x := range splitList(brand) {
			switch strings.ToLower(x) {
			case "yours", "own", "organic", "your-brand":
				c.BrandDisclose, c.BrandOrganic = true, true
			case "partner", "branded", "paid", "branded-content":
				c.BrandDisclose, c.BrandContent = true, true
			case "none", "-":
			default:
				return fmt.Errorf("TikTok commercial content %q: use yours, partner, both (yours,partner) or none", x)
			}
		}
		if t, ok := p.forValue("title-for", a); ok {
			p.plan.Titles[a.AccountID] = t
		}
	}
	format := p.plan.FormatFor(a)
	if p.wizard {
		who := fmt.Sprintf("TikTok · %s (@%s)", info.Nickname, info.Username)
		mode := map[bool]string{false: "direct", true: "draft"}[c.Draft]
		var allow, kinds []string
		if c.AllowComments {
			allow = append(allow, "comments")
		}
		if c.AllowDuet {
			allow = append(allow, "duet")
		}
		if c.AllowStitch {
			allow = append(allow, "stitch")
		}
		if c.BrandOrganic {
			kinds = append(kinds, "organic")
		}
		if c.BrandContent {
			kinds = append(kinds, "content")
		}
		title := p.plan.Titles[a.AccountID]
		err := runSteps(p.ask, []step{
			{run: func() error {
				return p.ask.form(huh.NewSelect[string]().Title(who).Description("How to send it to TikTok").Options(
					huh.NewOption("Post directly", "direct"),
					huh.NewOption("Send to my TikTok inbox as a draft (I finish it in the app)", "draft")).Value(&mode))
			}},
			{run: func() error {
				c.Draft = mode == "draft"
				if c.Draft {
					return nil
				}
				var opts []huh.Option[string]
				for _, o := range info.PrivacyLevelOptions {
					label := compose.TikTokPrivacyLabel[o]
					if label == "" {
						label = o
					}
					if !info.Audited && o != compose.TikTokSelfOnly {
						label += " (after TikTok's audit)"
					}
					opts = append(opts, huh.NewOption(label, o))
				}
				allowOpts := []huh.Option[string]{}
				if !info.CommentDisabled {
					allowOpts = append(allowOpts, huh.NewOption("Comments", "comments"))
				}
				if format == "VIDEO" {
					if !info.DuetDisabled {
						allowOpts = append(allowOpts, huh.NewOption("Duet", "duet"))
					}
					if !info.StitchDisabled {
						allowOpts = append(allowOpts, huh.NewOption("Stitch", "stitch"))
					}
				}
				for i := range allowOpts {
					allowOpts[i] = allowOpts[i].Selected(slices.Contains(allow, allowOpts[i].Value))
				}
				fields := []huh.Field{huh.NewSelect[string]().Title(who + " · who can view this post").Options(opts...).Value(&c.Privacy).Validate(func(v string) error {
					if v == "" {
						return errors.New("choose who can view it")
					}
					if !info.Audited && v != compose.TikTokSelfOnly {
						return errors.New(`until TikTok's audit is approved, posts must be "Only me"`)
					}
					return nil
				})}
				if len(allowOpts) > 0 {
					fields = append(fields, huh.NewMultiSelect[string]().Title("Allow").Options(allowOpts...).Value(&allow))
				}
				fields = append(fields, huh.NewConfirm().Title("Commercial content?").Description("Promotes yourself, a brand, product or service").Value(&c.BrandDisclose))
				return p.ask.form(fields...)
			}},
			{run: func() error {
				if c.Draft || !c.BrandDisclose {
					return nil
				}
				o1 := huh.NewOption("Your brand (you promote yourself or your business)", "organic").Selected(slices.Contains(kinds, "organic"))
				o2 := huh.NewOption("Branded content (a paid partnership with a third party)", "content").Selected(slices.Contains(kinds, "content"))
				return p.ask.form(huh.NewMultiSelect[string]().Title("What kind of commercial content").Options(o1, o2).Value(&kinds).Validate(func(v []string) error {
					if len(v) == 0 {
						return errors.New(`choose "Your brand", "Branded content" or both`)
					}
					return nil
				}))
			}},
			{run: func() error {
				if format != "PHOTO_POST" || c.Draft {
					return nil
				}
				if title == "" {
					title = p.plan.InternalTitle
				}
				return p.ask.form(huh.NewInput().Title("TikTok photo title").CharLimit(90).Value(&title))
			}},
		}, false)
		if err != nil {
			return err
		}
		c.AllowComments, c.AllowDuet, c.AllowStitch = slices.Contains(allow, "comments"), slices.Contains(allow, "duet"), slices.Contains(allow, "stitch")
		c.BrandOrganic, c.BrandContent = c.BrandDisclose && slices.Contains(kinds, "organic"), c.BrandDisclose && slices.Contains(kinds, "content")
		if format == "PHOTO_POST" && !c.Draft {
			p.plan.Titles[a.AccountID] = title
		}
	}
	var videoMs []int
	for _, it := range p.plan.FilesFor(a.AccountID) {
		if it.Media.Kind == "VIDEO" {
			videoMs = append(videoMs, it.Media.DurationMs)
		}
	}
	if probs := compose.TikTokProblems(c, info.Audited, info.PrivacyLevelOptions, info.MaxVideoPostDurationSec, videoMs); len(probs) > 0 {
		return fmt.Errorf("TikTok %s: %s", a.DisplayName, strings.Join(probs, "; "))
	}
	p.plan.TikTok[a.AccountID] = c
	return nil
}

// ------------------------------------------------------------------ when, review

func (p *poster) chooseWhen(ctx context.Context) error {
	when := strings.ToLower(strings.TrimSpace(firstNonEmpty(p.f.when, p.defaults.When, "draft")))
	if p.wizard {
		opts := []huh.Option[string]{huh.NewOption("Save as a draft (finish on the website)", "draft")}
		if p.me.Allows("SCHEDULE") {
			opts = append(opts, huh.NewOption("Next free posting slot", "next-slot"), huh.NewOption("At a time I choose", "at"))
		}
		if p.me.Allows("PUBLISH") {
			opts = append(opts, huh.NewOption("Publish now", "now"))
		}
		if p.whenChoice == "" {
			p.whenChoice = "draft"
			if slices.ContainsFunc(opts, func(o huh.Option[string]) bool { return o.Value == when }) {
				p.whenChoice = when
			} else if _, err := compose.LocalDateTime(when); err == nil && p.me.Allows("SCHEDULE") {
				p.whenChoice, p.whenAt = "at", when
			}
		}
		if p.whenAt == "" {
			p.whenAt = time.Now().Add(time.Hour).Format("2006-01-02 15:00")
		}
		desc := ""
		if p.me.RequiresApproval && p.me.Allows("SCHEDULE") {
			desc = "Scheduling and publishing go to an admin for approval first."
		}
		err := runSteps(p.ask, []step{
			{run: func() error {
				return p.ask.form(huh.NewSelect[string]().Title("When").Description(desc).Options(opts...).Value(&p.whenChoice))
			}},
			{run: func() error {
				if p.whenChoice != "at" {
					return nil
				}
				return p.ask.form(huh.NewInput().Title("Date and time (" + p.plan.Project.Timezone + ")").Description("YYYY-MM-DD HH:MM").Value(&p.whenAt).
					Validate(func(s string) error {
						local, err := compose.LocalDateTime(s)
						if err != nil {
							return err
						}
						if !compose.InFuture(local, p.plan.Project.Timezone, time.Now()) {
							return errors.New("that time has passed")
						}
						return nil
					}))
			}},
			{run: func() error {
				if p.whenChoice != "at" && p.whenChoice != "next-slot" {
					return nil
				}
				if p.repeatChoice == "" {
					p.repeatChoice = firstNonEmpty(p.f.repeat, "no")
				}
				return p.ask.form(huh.NewSelect[string]().Title("Repeat it?").Description("the same post again, every …").Options(
					huh.NewOption("No, once", "no"), huh.NewOption("Every day", "daily"), huh.NewOption("Every week", "weekly"),
					huh.NewOption("Every 2 weeks", "14"), huh.NewOption("Every 30 days", "30")).Value(&p.repeatChoice),
					huh.NewInput().Title("Until (optional)").Description("YYYY-MM-DD, the last day · empty = until you stop it (mktauto stop-repeat)").
						Value(&p.repeatUntil).Validate(validDay))
			}},
		}, false)
		if err != nil {
			return err
		}
		when = p.whenChoice
		if when == "at" {
			when = p.whenAt
		}
	}
	switch when {
	case "draft", "":
		p.plan.When = compose.When{Kind: "DRAFT"}
	case "now":
		if !p.me.Allows("PUBLISH") {
			return withCode(exitNotAllowed, errors.New("this terminal may not publish now (create a login code that allows it, or schedule instead)"))
		}
		p.plan.When = compose.When{Kind: "NOW"}
	case "next-slot":
		if !p.me.Allows("SCHEDULE") {
			return withCode(exitNotAllowed, errors.New("this terminal may only save drafts"))
		}
		if p.f.dryRun {
			p.plan.When = compose.When{Kind: "NEXT_SLOT"}
			return nil
		}
		slot, err := p.c.NextSlot(ctx, p.plan.Project.ID)
		if err != nil {
			return err
		}
		p.plan.When = compose.When{Kind: "AT", At: strings.Replace(slot.LocalDateTime[:16], "T", " ", 1)}
		if !p.wizard {
			p.env.printf("Next free slot: %s (%s)\n", bold.Render(p.plan.When.At), p.plan.Project.Timezone)
		}
	default:
		if !p.me.Allows("SCHEDULE") {
			return withCode(exitNotAllowed, errors.New("this terminal may only save drafts"))
		}
		local, err := compose.LocalDateTime(when)
		if err != nil {
			return fmt.Errorf("--when: %w", err)
		}
		if !compose.InFuture(local, p.plan.Project.Timezone, time.Now()) {
			return fmt.Errorf("%s has passed in %s", when, p.plan.Project.Timezone)
		}
		p.plan.When = compose.When{Kind: "AT", At: when}
	}
	choice, until := p.f.repeat, p.f.repeatUntil
	if p.wizard {
		choice, until = p.repeatChoice, p.repeatUntil
	}
	days, err := parseRepeat(choice)
	if err != nil {
		return fmt.Errorf("--repeat: %w", err)
	}
	if err := validDay(until); err != nil {
		return fmt.Errorf("--repeat-until: %w", err)
	}
	p.plan.RepeatDays, p.plan.RepeatUntil = 0, ""
	if days > 0 {
		if p.plan.When.Kind != "AT" && p.plan.When.Kind != "NEXT_SLOT" {
			return errors.New("--repeat needs a time: --when \"YYYY-MM-DD HH:MM\" or next-slot")
		}
		p.plan.RepeatDays, p.plan.RepeatUntil = days, strings.TrimSpace(until)
	}
	return nil
}

// parseRepeat: no, daily, weekly, biweekly, monthly (30 days), or a number of days (7 or 7d), 1 to 365.
func parseRepeat(s string) (int, error) {
	switch v := strings.ToLower(strings.TrimSpace(s)); v {
	case "", "no", "none", "never", "once":
		return 0, nil
	case "daily", "day":
		return 1, nil
	case "weekly", "week":
		return 7, nil
	case "biweekly", "fortnightly":
		return 14, nil
	case "monthly", "month":
		return 30, nil
	default:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSuffix(v, "d"), "%d", &n); err != nil || n < 1 || n > 365 || strings.TrimLeft(strings.TrimSuffix(v, "d"), "0123456789") != "" {
			return 0, fmt.Errorf("%q: use daily, weekly, biweekly, monthly or a number of days (1–365)", s)
		}
		return n, nil
	}
}

func validDay(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(s)); err != nil {
		return errors.New("write the day as YYYY-MM-DD")
	}
	return nil
}

func (p *poster) actionLabel() string {
	switch p.plan.When.Kind {
	case "AT":
		return "Schedule it for " + p.plan.When.At + " (" + p.plan.Project.Timezone + ")"
	case "NOW":
		return "Publish now"
	}
	return "Save the draft"
}

// preview: each account's final text (caption, then hashtags, as the server composes it), title and first comment,
// with its length against the platform's limit.
func (p *poster) preview() string {
	plan := p.plan
	var b strings.Builder
	for _, a := range plan.Accounts {
		caption := plan.Caption
		if c, ok := plan.Captions[a.AccountID]; ok {
			caption = c
		}
		text := compose.FinalText(caption, plan.Hashtags)
		n, limit := compose.TextLength(a.Platform, text), compose.MaxText(a.Platform, plan.FormatFor(a))
		count := fmt.Sprintf("%s characters", humanize.Comma(int64(n)))
		if limit > 0 && limit < 10000 {
			count += " of " + humanize.Comma(int64(limit))
			if n > limit {
				count = red.Render(count + " · too long")
			}
		}
		b.WriteString(bold.Render(accountLabel(a)) + "  " + faint.Render(compose.FormatLabel[plan.FormatFor(a)]+" · "+count) + "\n")
		if t := strings.TrimSpace(plan.Titles[a.AccountID]); t != "" {
			b.WriteString(faint.Render("Title: ") + t + "\n")
		}
		if strings.TrimSpace(text) == "" {
			text = faint.Render("(no text)")
		}
		b.WriteString(text + "\n")
		for i, part := range plan.Threads[a.AccountID] {
			b.WriteString(faint.Render(fmt.Sprintf("↳ reply %d · %d characters", i+1, compose.TextLength(a.Platform, part))) + "\n" + part + "\n")
		}
		if fc := strings.TrimSpace(plan.FirstComment); fc != "" && compose.CommentsOn(a.Platform, plan.FormatFor(a)) {
			b.WriteString(faint.Render("First comment: ") + fc + "\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// summary of the whole post, shown before anything is saved.
func (p *poster) summary() string {
	plan := p.plan
	var b strings.Builder
	fmt.Fprintf(&b, "Project %s", plan.Project.Name)
	if n := len(p.files); n > 0 {
		var names []string
		for _, f := range p.files {
			names = append(names, f.Name)
		}
		fmt.Fprintf(&b, " · %s: %s", plural(n, "file"), truncate(strings.Join(names, ", "), 70))
	} else {
		b.WriteString(" · text only")
	}
	if p.cover != nil {
		fmt.Fprintf(&b, " · cover %s", p.cover.Name)
	}
	b.WriteString("\n")
	for _, a := range plan.Accounts {
		format := compose.FormatLabel[plan.FormatFor(a)]
		if format == "" {
			format = "?"
		}
		caption := "main caption"
		if _, own := plan.Captions[a.AccountID]; own {
			caption = "own caption"
		}
		extra := []string{plural(len(plan.FilesFor(a.AccountID)), "file"), caption}
		if n := len(plan.Threads[a.AccountID]); n > 0 {
			extra = append(extra, fmt.Sprintf("thread of %d", n+1))
		}
		if plan.AIFor(a.AccountID) && compose.AILabelSupported(a.Platform, plan.TikTok[a.AccountID].Draft) {
			extra = append(extra, "AI label")
		}
		if c, ok := plan.TikTok[a.AccountID]; ok && c.Draft {
			extra = append(extra, "TikTok inbox draft")
		}
		fmt.Fprintf(&b, "  %-34s %-11s %s\n", truncate(accountLabel(a), 34), format, faint.Render(strings.Join(extra, " · ")))
	}
	if c := strings.TrimSpace(plan.Caption); c != "" {
		fmt.Fprintf(&b, "Caption: %s\n", faint.Render(truncate(strings.ReplaceAll(c, "\n", " "), 90)))
	}
	if plan.RepeatDays > 0 {
		until := "until you stop it"
		if plan.RepeatUntil != "" {
			until = "until " + plan.RepeatUntil
		}
		fmt.Fprintf(&b, "Repeats: every %s, %s\n", plural(plan.RepeatDays, "day"), until)
	}
	if len(plan.Hashtags) > 0 {
		fmt.Fprintf(&b, "Hashtags: %s\n", faint.Render("#"+strings.Join(plan.Hashtags, " #")))
	}
	if over := p.tooLong(func(a api.ProjectAccount) string {
		if c, ok := plan.Captions[a.AccountID]; ok {
			return c
		}
		return plan.Caption
	}); len(over) > 0 {
		for _, a := range plan.Accounts {
			if l, ok := over[a.AccountID]; ok {
				b.WriteString(yellow.Render("! "+l) + "\n")
			}
		}
	}
	if p.plan.When.Kind != "DRAFT" && p.tiktokDirect() {
		b.WriteString(faint.Render("By posting to TikTok, you agree to TikTok's Music Usage Confirmation ("+compose.TikTokMusicUsageURL+")") + "\n")
		if p.tiktokBranded() {
			b.WriteString(faint.Render("and Branded Content Policy ("+compose.TikTokBrandedPolicyURL+")") + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p *poster) tiktokDirect() bool {
	for _, a := range p.plan.Accounts {
		if c, ok := p.plan.TikTok[a.AccountID]; ok && !c.Draft {
			return true
		}
	}
	return false
}

func (p *poster) tiktokBranded() bool {
	for _, a := range p.plan.Accounts {
		if c, ok := p.plan.TikTok[a.AccountID]; ok && !c.Draft && c.BrandContent {
			return true
		}
	}
	return false
}

// confirm: the last screen before anything is saved. Esc goes back to change something.
func (p *poster) confirm() error {
	if !p.wizard {
		return nil
	}
	if probs := p.plan.Problems(); len(probs) > 0 {
		p.env.printf("%s\n", red.Render("✗ "+strings.Join(probs, "\n  ")))
		return errBack
	}
	choice := "go"
	opts := []huh.Option[string]{huh.NewOption(p.actionLabel(), "go")}
	if p.plan.When.Kind != "DRAFT" {
		opts = append(opts, huh.NewOption("Save it as a draft instead", "draft"))
	}
	opts = append(opts, huh.NewOption("Preview the exact text each account gets", "preview"))
	for {
		if err := p.ask.form(
			huh.NewNote().Title("Review").Description(p.summary()),
			huh.NewSelect[string]().Title("Ready?").Description("esc goes back to change anything").Options(opts...).Value(&choice),
		); err != nil {
			return err
		}
		if choice != "preview" {
			break
		}
		p.ask.asked-- // the preview is not a question to come back to
		_, _ = p.ask.run(huh.NewForm(huh.NewGroup(huh.NewNote().Title("What each account gets").Description(p.preview()).Next(true).NextLabel("Back"))).WithShowHelp(true))
		choice = "go"
	}
	if choice == "draft" {
		p.plan.When = compose.When{Kind: "DRAFT"}
		p.whenChoice = "draft"
	}
	return nil
}

// ------------------------------------------------------------------ save, check, act

func (p *poster) saveAndAct(ctx context.Context) error {
	e := p.env
	in, err := p.plan.Input()
	if err != nil {
		return err
	}
	if p.f.dryRun {
		e.printf("%s\n\n", p.preview())
		e.printf("%s Nothing was uploaded or saved. This is the post that would be sent:\n", yellow.Render("Dry run:"))
		enc := json.NewEncoder(e.dataOut())
		enc.SetIndent("", "  ")
		return enc.Encode(in)
	}
	post, err := p.c.CreatePost(ctx, in)
	if err != nil {
		return err
	}
	v, err := p.c.Validate(ctx, post.ID)
	if err != nil {
		return err
	}
	if fresh, err := p.c.Post(ctx, post.ID); err == nil {
		post = fresh
	}
	p.review(post, v)
	if v.ErrorCount > 0 {
		e.printf("\n%s It's saved as a draft: fix it on the website (mktauto open %s) or change the files and try again.\n", red.Render("Can't publish yet."), post.ID)
		p.remember(post, "draft")
		return withCode(exitInvalid, errSilent)
	}
	if !p.wizard && p.plan.When.Kind != "DRAFT" && p.tiktokDirect() {
		e.printf("\nBy posting to TikTok, you agree to TikTok's Music Usage Confirmation (%s)", faint.Render(compose.TikTokMusicUsageURL))
		if p.tiktokBranded() {
			e.printf(" and Branded Content Policy (%s)", faint.Render(compose.TikTokBrandedPolicyURL))
		}
		e.printf(".\n")
	}
	// The wizard already asked; ask again only when the server found something worth a look.
	if p.wizard && !p.f.yes && p.plan.When.Kind != "DRAFT" && hasWarnings(v) {
		ok := true
		if err := huh.NewConfirm().Title(p.actionLabel() + " despite the warnings above?").Affirmative("Yes").Negative("No, keep it as a draft").Value(&ok).Run(); err != nil || !ok {
			p.plan.When.Kind = "DRAFT"
		}
	}
	result := post
	label := "draft"
	switch p.plan.When.Kind {
	case "AT":
		result, err = p.c.Schedule(ctx, post.ID, nil)
		label = "scheduled"
	case "NOW":
		result, err = p.c.PublishNow(ctx, post.ID)
		label = "publishing"
	}
	if err != nil {
		var prob *api.Problem
		if errors.As(err, &prob) && prob.Validation != nil {
			p.review(post, prob.Validation)
		}
		p.remember(post, "draft")
		return fmt.Errorf("%v (the post is saved as a draft)", err)
	}
	if result.Status == "PENDING_APPROVAL" {
		label = "approval"
	}
	p.remember(result, label)
	p.result = result
	defer p.costLine(ctx, result.ID)
	switch label {
	case "draft":
		e.printf("%s Draft saved. Finish it on the website: mktauto open %s\n", green.Render("✓"), result.ID)
	case "scheduled":
		e.printf("%s Scheduled for %s (%s)\n", green.Render("✓"), p.plan.When.At, p.plan.Project.Timezone)
	case "approval":
		e.printf("%s Sent for approval: it goes out once an admin approves it.\n", green.Render("✓"))
	case "publishing":
		e.printf("%s Publishing…\n", green.Render("✓"))
		if p.wizard || p.f.wait {
			result = p.watch(ctx, result.ID)
		}
	}
	if p.f.jsonOut {
		enc := json.NewEncoder(e.dataOut())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	return nil
}

func hasWarnings(v *api.Validation) bool {
	for _, d := range v.Destinations {
		if len(d.Issues) > 0 {
			return true
		}
	}
	return false
}

func (p *poster) review(post *api.Post, v *api.Validation) {
	e := p.env
	issues := map[string][]api.Issue{}
	for _, d := range v.Destinations {
		issues[d.DestinationID] = d.Issues
	}
	e.printf("\n%s\n", bold.Render("Review"))
	for _, d := range post.Destinations {
		files := len(d.MediaIDs)
		if files == 0 {
			files = len(post.Media)
		}
		e.printf("  %-14s %-24s %-10s %s\n", compose.PlatformLabel[d.Platform], truncate(d.AccountName, 24), compose.FormatLabel[d.Format],
			faint.Render(plural(files, "file")+" · "+truncate(strings.ReplaceAll(d.FinalText, "\n", " "), 50)))
		for _, i := range issues[d.ID] {
			mark := yellow.Render("    ! ")
			if i.Severity == "ERROR" {
				mark = red.Render("    ✗ ")
			}
			e.printf("%s%s\n", mark, i.Message)
		}
	}
}

// watch shows each account's result until they're all done (or for 5 minutes; Ctrl+C stops watching, not the post).
func (p *poster) watch(ctx context.Context, id string) *api.Post {
	deadline := time.Now().Add(5 * time.Minute)
	var last *api.Post
	shown := map[string]string{}
	for time.Now().Before(deadline) {
		post, err := p.c.Post(ctx, id)
		if err == nil {
			last = post
			busy := false
			for _, d := range post.Destinations {
				if shown[d.ID] != d.Status {
					shown[d.ID] = d.Status
					extra := ""
					if d.PublishedURL != "" {
						extra = "  " + faint.Render(d.PublishedURL)
					} else if d.LastErrorMessage != "" && (d.Status == "FAILED" || d.Status == "RETRY_WAIT") {
						extra = "  " + red.Render(truncate(d.LastErrorMessage, 70))
					}
					p.env.printf("  %-14s %-24s %s%s\n", compose.PlatformLabel[d.Platform], truncate(d.AccountName, 24), statusText(d.Status), extra)
				}
				if !doneStatuses[d.Status] {
					busy = true
				}
			}
			if !busy {
				return post
			}
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(3 * time.Second):
		}
	}
	p.env.printf("%s\n", faint.Render("Still working: check later with mktauto status "+id))
	return last
}

func (p *poster) remember(post *api.Post, action string) {
	files := make([]history.File, len(p.files))
	for i, f := range p.files {
		files[i] = history.File{Name: f.Name, SHA256: f.SHA256}
	}
	accounts := make([]string, len(p.plan.Accounts))
	for i, a := range p.plan.Accounts {
		accounts[i] = compose.PlatformLabel[a.Platform] + " " + a.DisplayName
	}
	_ = history.Add(history.Entry{At: time.Now(), Folder: p.env.Dir, PostID: post.ID, Project: p.plan.Project.Name, Action: action,
		When: p.plan.When.At, Accounts: accounts, Files: files, Server: p.env.Server})
	p.env.Config.LastProject[p.env.Dir] = p.plan.Project.ID
	_ = p.env.Config.Save()
}

// ------------------------------------------------------------------ helpers

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func mustMatch(terms []string, accounts []api.ProjectAccount) []api.ProjectAccount {
	if len(terms) == 0 {
		return nil
	}
	m, _ := compose.MatchAccounts(terms, accounts)
	return m
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// parseForFlags checks every ACCOUNT=value flag against the chosen accounts (scripts only).
func (p *poster) parseForFlags() error {
	p.forv = map[string]map[string]string{}
	for name, specs := range p.f.perAccountFlags() {
		for _, spec := range *specs {
			acc, value, ok := strings.Cut(spec, "=")
			if !ok || strings.TrimSpace(acc) == "" {
				return fmt.Errorf("--%s %q: write ACCOUNT=value, e.g. --%s instagram=…", name, spec, name)
			}
			matched, err := compose.MatchAccounts([]string{strings.TrimSpace(acc)}, p.chosen)
			if err != nil {
				return fmt.Errorf("--%s: %w (it must be one of the accounts in --accounts)", name, err)
			}
			if p.forv[name] == nil {
				p.forv[name] = map[string]string{}
			}
			for _, a := range matched {
				p.forv[name][a.AccountID] = value
			}
		}
	}
	return nil
}

func (p *poster) forValue(flag string, a api.ProjectAccount) (string, bool) {
	v, ok := p.forv[flag][a.AccountID]
	return v, ok
}

// postHelp: mktauto post -h, grouped (TestPostHelpListsEveryFlag keeps it complete).
const postHelp = `Usage: mktauto post [flags]
Without flags in a terminal: step by step. With flags: everything from the command line (scripts, CI).
ACCOUNT = an account name, @handle, id or platform (facebook, instagram, threads, x, pinterest, tiktok, youtube).

What and where
  --project NAME              project (name or id)
  --accounts A,B              accounts to post to (group:NAME = an account group saved on the website)
  --template NAME             start from a saved template (caption, hashtags, link, first comment, accounts)
  --preset NAME               a set of these flags saved with mktauto preset save (flags given here win)
  --files a.png,b.mp4         files in posting order ("all" = every file; none = text only)
  --only ACCOUNT=1,3          that account gets only these files (positions in --files), in this order; repeatable

Texts (for every account)
  --caption TEXT  | --caption-file FILE
  --hashtags "android kotlin"
  --hashtag-set A,B           add the hashtags of saved sets
  --link URL                  link preview on Facebook and Threads, the Pin's link
  --first-comment TEXT        Facebook Pages and Instagram
  --alt FILE=TEXT             an image's alt text (for people who can't see it); repeatable
  --alt-ai                    the AI writes alt text for the images without one (it looks at them)
  --thread-for A,B            X / Threads accounts whose too-long caption becomes a thread of replies
                              (or put a line with --- between the parts of an own caption)
  --title TEXT                YouTube, Pinterest and TikTok photo title
  --name TEXT                 internal name, only you see it (default: --title)
  --ai                        "made with AI" label wherever the platform has one
  --ai-off-for A,B            …but not on these accounts

One account's own value (ACCOUNT=value, repeatable)
  --caption-for ACCOUNT=TEXT        --caption-file-for ACCOUNT=FILE
  --title-for ACCOUNT=TEXT          --youtube-privacy-for ACCOUNT=unlisted
  --board-for ACCOUNT=BOARD_ID      --pin-link-for ACCOUNT=URL
  --tiktok-privacy-for ACCOUNT=SELF_ONLY
  --tiktok-allow-for ACCOUNT=comments,duet
  --tiktok-brand-for ACCOUNT=yours,partner

Platforms
  --tiktok-privacy LEVEL      PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, FOLLOWER_OF_CREATOR or SELF_ONLY
  --tiktok-allow LIST         comments,duet,stitch (off unless listed)
  --tiktok-brand LIST         commercial content: yours (your brand), partner (branded content)
  --tiktok-draft              send to the TikTok inbox as a draft
  --tiktok-draft-for A,B      …only these TikTok accounts
  --youtube-privacy P         public (default), unlisted or private
  --board BOARD_ID            Pinterest board (mktauto boards ACCOUNT lists them)
  --pin-link URL              the Pin's link (default: --link)
  --cover IMAGE               an image in this folder as the video's cover
  --cover-frame SECONDS       a second of the video as its cover (TikTok, Instagram, Pinterest)

When and how
  --when WHEN                 draft (default), now, next-slot or "YYYY-MM-DD HH:MM" (project's timezone)
  --repeat EVERY              post it again: daily, weekly, biweekly, monthly or N days (needs a time)
  --repeat-until YYYY-MM-DD   the last day of the repeats (default: until mktauto stop-repeat)
  --duplicates WHAT           files posted before: post, new-only or skip
  --dry-run                   check everything, upload and save nothing, print the post
  --json                      print the saved post as JSON
  --yes                       don't ask for confirmation
  --wait                      after publishing now, wait for each account's result

Examples
  mktauto post --project Memoshot --accounts facebook,instagram,tiktok --files a.png,b.png,c.png \
    --only tiktok=1 --caption-file caption.txt --caption-for x="Short version for X" \
    --tiktok-privacy SELF_ONLY --when "2026-12-01 18:30"
  mktauto post --project Memoshot --accounts youtube,tiktok --files reel.mp4 --title "Launch" \
    --youtube-privacy unlisted --tiktok-brand yours --tiktok-privacy PUBLIC_TO_EVERYONE --when now --wait
`

// filePath: an absolute path as it is, else relative to the folder.
func filePath(dir, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(dir, name)
}

// splitFor cuts text into a thread for the account (room left in the first post for the hashtags).
func (p *poster) splitFor(a api.ProjectAccount, text string) []string {
	limit := compose.MaxText(a.Platform, p.plan.FormatFor(a))
	reserve := p.finalLength(a.Platform, "") + 2
	return compose.SplitThread(a.Platform, text, limit, reserve)
}

func platformShortName(p string) string {
	return map[string]string{"X": "x", "THREADS": "threads"}[p]
}
