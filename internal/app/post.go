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
	project, accounts, files                         string
	only                                             multiFlag
	caption, captionFile, hashtags, link, firstComm  string
	title                                            string
	ai                                               bool
	aiOff                                            string
	tiktokPrivacy, tiktokAllow                       string
	tiktokDraft                                      bool
	youtubePrivacy, board                            string
	cover                                            string
	coverFrame                                       float64
	when, duplicates                                 string
	dryRun, jsonOut, yes, wait                       bool
}

// post: the wizard when run in a terminal without flags, otherwise everything from flags (scripts, CI).
func (e *Env) post(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var f postFlags
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
	fs.StringVar(&f.when, "when", "draft", "draft, now, next-slot, or \"YYYY-MM-DD HH:MM\" in the project's timezone")
	fs.StringVar(&f.duplicates, "duplicates", "", "files that were posted before: post (anyway), new-only (only accounts that haven't had them), skip")
	fs.BoolVar(&f.dryRun, "dry-run", false, "check everything and print the post that would be sent, without uploading or saving")
	fs.BoolVar(&f.jsonOut, "json", false, "print the saved post as JSON (messages go to stderr)")
	fs.BoolVar(&f.yes, "yes", false, "don't ask for confirmation")
	fs.BoolVar(&f.wait, "wait", false, "after publishing now, wait for the result")
	if err := fs.Parse(args); err != nil {
		return err
	}
	flagMode := !e.Interactive || f.project != "" || f.accounts != "" || f.files != ""
	if f.jsonOut {
		e.Out = e.Err
	}
	p := &poster{env: e, f: f, wizard: !flagMode}
	return p.run(ctx)
}

type poster struct {
	env    *Env
	f      postFlags
	wizard bool
	c      *api.Client
	me     *api.Me
	plan   *compose.Plan
	files  []media.File        // chosen, in order
	cover  *media.File         // cover image (not posted as media)
	dups   map[string]api.Duplicate
	tiktok map[string]*api.TikTokCreatorInfo
}

func (p *poster) run(ctx context.Context) error {
	e := p.env
	var err error
	if p.c, err = e.client(); err != nil {
		return err
	}
	if p.me, err = e.me(ctx, p.c); err != nil {
		return err
	}
	defaults, err := media.LoadDefaults(e.Dir)
	if err != nil {
		return fmt.Errorf("mktauto.yml: %w", err)
	}
	all, err := media.Scan(e.Dir)
	if err != nil {
		return err
	}
	p.plan = compose.NewPlan()
	p.tiktok = map[string]*api.TikTokCreatorInfo{}

	if err := p.chooseProject(ctx, defaults); err != nil {
		return err
	}
	accounts, err := p.c.ProjectAccounts(ctx, p.plan.Project.ID)
	if err != nil {
		return err
	}
	slices.SortFunc(accounts, func(a, b api.ProjectAccount) int { return a.SortOrder - b.SortOrder })
	if err := p.chooseAccounts(accounts, defaults); err != nil {
		return err
	}
	if err := p.chooseFiles(all); err != nil {
		return err
	}
	if err := p.checkDuplicates(ctx); err != nil {
		return err
	}
	if err := p.chooseCover(all); err != nil {
		return err
	}
	if err := p.uploadFiles(ctx); err != nil {
		return err
	}
	if err := p.choosePerAccount(); err != nil {
		return err
	}
	if err := p.details(ctx, defaults); err != nil {
		return err
	}
	if err := p.chooseWhen(ctx, defaults); err != nil {
		return err
	}
	return p.saveAndAct(ctx)
}

// ------------------------------------------------------------------ project, accounts, files

func (p *poster) chooseProject(ctx context.Context, d *media.Defaults) error {
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
	want := p.f.project
	if want == "" {
		want = d.Project
	}
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
	if pr := find(want); pr != nil {
		chosen = pr.ID
	} else if last := p.env.Config.LastProject[p.env.Dir]; find(last) != nil {
		chosen = last
	}
	if len(live) == 1 {
		chosen = live[0].ID
		p.env.printf("Project: %s\n", bold.Render(live[0].Name))
	} else {
		opts := make([]huh.Option[string], len(live))
		for i, pr := range live {
			opts[i] = huh.NewOption(pr.Name, pr.ID)
		}
		if err := huh.NewSelect[string]().Title("Project").Options(opts...).Value(&chosen).Run(); err != nil {
			return err
		}
	}
	p.plan.Project = *find(chosen)
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

func (p *poster) chooseAccounts(accounts []api.ProjectAccount, d *media.Defaults) error {
	if len(accounts) == 0 {
		return errors.New("this project has no accounts: link some on the website (Projects)")
	}
	if !p.wizard {
		terms := splitList(p.f.accounts)
		if len(terms) == 0 {
			terms = d.Accounts
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
		p.plan.Accounts = chosen
		return nil
	}
	pre := map[string]bool{}
	if len(d.Accounts) > 0 {
		if m, err := compose.MatchAccounts(d.Accounts, accounts); err == nil {
			for _, a := range m {
				pre[a.AccountID] = true
			}
		}
	} else {
		for _, a := range accounts {
			pre[a.AccountID] = a.DefaultSelected
		}
	}
	var opts []huh.Option[string]
	var blocked []string
	for _, a := range accounts {
		if !a.Usable() {
			blocked = append(blocked, accountLabel(a)+" ("+strings.ToLower(strings.ReplaceAll(a.Status, "_", " "))+")")
			continue
		}
		opts = append(opts, huh.NewOption(accountLabel(a), a.AccountID).Selected(pre[a.AccountID]))
	}
	if len(blocked) > 0 {
		p.env.printf("%s\n", faint.Render("Not available now: "+strings.Join(blocked, ", ")))
	}
	if len(opts) == 0 {
		return errors.New("none of this project's accounts can post right now: reconnect them on the website")
	}
	var ids []string
	if err := huh.NewMultiSelect[string]().Title("Accounts").Description("space to tick, enter to continue").Options(opts...).Value(&ids).
		Validate(func(v []string) error {
			if len(v) == 0 {
				return errors.New("tick at least one account")
			}
			return nil
		}).Run(); err != nil {
		return err
	}
	for _, a := range accounts {
		if slices.Contains(ids, a.AccountID) {
			p.plan.Accounts = append(p.plan.Accounts, a)
		}
	}
	return nil
}

func fileLabel(f media.File) string {
	s := f.Name + "  " + faint.Render(humanize.Bytes(uint64(f.Size)))
	if f.Width > 0 {
		s += faint.Render(fmt.Sprintf(" · %d×%d", f.Width, f.Height))
	}
	if f.IsVideo() {
		s += faint.Render(" · video")
	}
	return s
}

func (p *poster) chooseFiles(all []media.File) error {
	if !p.wizard {
		spec := strings.TrimSpace(p.f.files)
		if spec == "" {
			return nil // text only
		}
		if spec == "all" {
			p.files = all
			return nil
		}
		for _, name := range splitList(spec) {
			f, ok := media.Describe(filepath.Join(p.env.Dir, name))
			if !ok {
				return fmt.Errorf("%s: not found, or not an image/video MKTAuto accepts (JPEG, PNG, WebP, GIF, MP4, MOV, WebM)", name)
			}
			p.files = append(p.files, f)
		}
		return nil
	}
	if len(all) == 0 {
		p.env.printf("%s\n", faint.Render("No images or videos in this folder: this will be a text post."))
		return nil
	}
	opts := make([]huh.Option[int], len(all))
	for i, f := range all {
		opts[i] = huh.NewOption(fileLabel(f), i).Selected(len(all) <= 10)
	}
	var picked []int
	if err := huh.NewMultiSelect[int]().Title("Files").Description("tick the images and videos for this post (none = text only)").
		Options(opts...).Value(&picked).Height(min(len(all)+4, 18)).Run(); err != nil {
		return err
	}
	chosen := make([]media.File, len(picked))
	labels := make([]string, len(picked))
	for i, idx := range picked {
		chosen[i], labels[i] = all[idx], all[idx].Name
	}
	order, err := ui.Order("Order: the first file shows first", labels)
	if err != nil {
		return err
	}
	for _, i := range order {
		p.files = append(p.files, chosen[i])
	}
	return nil
}

// ------------------------------------------------------------------ already posted?

func (p *poster) checkDuplicates(ctx context.Context) error {
	if len(p.files) == 0 {
		return nil
	}
	p.env.printf("%s\n", faint.Render(fmt.Sprintf("Checking %s…", plural(len(p.files), "file"))))
	if err := media.Fingerprint(p.files, nil); err != nil {
		return err
	}
	hashes := make([]string, len(p.files))
	for i, f := range p.files {
		hashes[i] = f.SHA256
	}
	found, err := p.c.Duplicates(ctx, hashes)
	if err != nil {
		return err
	}
	p.dups = map[string]api.Duplicate{}
	for _, d := range found {
		p.dups[d.SHA256] = d
	}
	var keep []media.File
	exclude := map[int][]string{} // index in keep → accounts that already had it
	for _, f := range p.files {
		d, ok := p.dups[f.SHA256]
		uses := usedSomewhere(d.Uses)
		if !ok || len(uses) == 0 {
			keep = append(keep, f)
			continue
		}
		var had []string
		for _, a := range p.plan.Accounts {
			for _, u := range uses {
				if u.AccountID == a.AccountID {
					had = append(had, a.AccountID)
					break
				}
			}
		}
		p.env.printf("%s %s was used before:\n", yellow.Render("!"), bold.Render(f.Name))
		for i, u := range uses {
			if i == 5 {
				p.env.printf("    … and %d more\n", len(uses)-5)
				break
			}
			when := ""
			switch {
			case u.PublishedAt != nil:
				when = "posted " + u.PublishedAt.Local().Format("2 Jan 2006")
			case u.ScheduledAt != nil:
				when = strings.ToLower(u.Status) + " for " + u.ScheduledAt.Local().Format("2 Jan 2006 15:04")
			default:
				when = strings.ToLower(strings.ReplaceAll(u.Status, "_", " "))
			}
			p.env.printf("    %s · %s · %s  %s\n", compose.PlatformLabel[u.Platform], u.AccountName, when, faint.Render(truncate(u.PostTitle, 40)))
		}
		choice, err := p.duplicateChoice(f, had)
		if err != nil {
			return err
		}
		switch choice {
		case "skip":
			p.env.printf("    %s\n", faint.Render("left out"))
		case "new-only":
			if len(had) == len(p.plan.Accounts) {
				p.env.printf("    %s\n", faint.Render("every chosen account already had it: left out"))
				continue
			}
			exclude[len(keep)] = had
			keep = append(keep, f)
		default:
			keep = append(keep, f)
		}
	}
	p.files = keep
	// Start the per-account selection from those choices.
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
	// An account that already had every file is left out of this post (rather than posting it without them).
	if len(p.files) > 0 {
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
			return errors.New("every chosen account already had these files: nothing to post")
		}
		p.plan.Accounts = keepAccounts
	}
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

func (p *poster) duplicateChoice(f media.File, had []string) (string, error) {
	if !p.wizard {
		switch p.f.duplicates {
		case "post", "new-only", "skip":
			return p.f.duplicates, nil
		}
		return "", fmt.Errorf("%s was posted before: add --duplicates=post (anyway), new-only (only accounts that haven't had it) or skip", f.Name)
	}
	choice := "new-only"
	opts := []huh.Option[string]{}
	if len(had) > 0 && len(had) < len(p.plan.Accounts) {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Only to the %s that haven't had it", plural(len(p.plan.Accounts)-len(had), "account")), "new-only"))
	} else {
		choice = "skip"
	}
	opts = append(opts, huh.NewOption("Leave this file out", "skip"), huh.NewOption("Post it again to every chosen account", "post"))
	err := huh.NewSelect[string]().Title("What should happen with " + f.Name + "?").Options(opts...).Value(&choice).Run()
	return choice, err
}

// ------------------------------------------------------------------ cover

func (p *poster) chooseCover(all []media.File) error {
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
	for _, f := range all {
		if !f.IsVideo() && !slices.ContainsFunc(p.files, func(x media.File) bool { return x.Path == f.Path }) {
			images = append(images, f)
		}
	}
	choice := "none"
	opts := []huh.Option[string]{huh.NewOption("Platform default", "none"), huh.NewOption("A second of the video", "frame")}
	if len(images) > 0 {
		opts = append(opts, huh.NewOption("An image from this folder (Facebook, Instagram Reels, YouTube, Pinterest)", "image"))
	}
	if err := huh.NewSelect[string]().Title("Video cover").Options(opts...).Value(&choice).Run(); err != nil {
		return err
	}
	switch choice {
	case "image":
		idx := 0
		imgOpts := make([]huh.Option[int], len(images))
		for i, f := range images {
			imgOpts[i] = huh.NewOption(fileLabel(f), i)
		}
		if err := huh.NewSelect[int]().Title("Cover image").Options(imgOpts...).Value(&idx).Run(); err != nil {
			return err
		}
		c := images[idx]
		p.cover = &c
		fallthrough
	case "frame":
		if choice == "image" {
			// TikTok can only use a frame: ask for one too.
			if !slices.ContainsFunc(p.plan.Accounts, func(a api.ProjectAccount) bool { return a.Platform == "TIKTOK" }) {
				return nil
			}
		}
		s := ""
		if err := huh.NewInput().Title("Second of the video for the cover").Description("e.g. 3 or 4.5 · used where an image isn't possible (TikTok)").
			Value(&s).Validate(func(v string) error {
				if strings.TrimSpace(v) == "" && choice == "image" {
					return nil
				}
				x, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil || x < 0 {
					return errors.New("enter seconds, e.g. 3 or 4.5")
				}
				return nil
			}).Run(); err != nil {
			return err
		}
		if x, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			p.plan.CoverFrameMs = int(x * 1000)
		}
	}
	return nil
}

// ------------------------------------------------------------------ upload

func (p *poster) uploadFiles(ctx context.Context) error {
	all := append([]media.File{}, p.files...)
	if p.cover != nil {
		if p.cover.SHA256 == "" {
			p.cover.SHA256, _ = media.SHA256(p.cover.Path)
		}
		all = append(all, *p.cover)
	}
	items := make([]compose.Item, len(all))
	if p.f.dryRun {
		// Nothing leaves this computer: use what's known locally (video length is measured by the server after upload).
		for i, f := range all {
			items[i] = compose.Item{File: f, Media: api.Media{ID: "(" + f.Name + ")", Kind: f.Kind, Width: f.Width, Height: f.Height, SizeBytes: f.Size}}
		}
	} else if len(all) > 0 {
		names := make([]string, len(all))
		sizes := make([]int64, len(all))
		for i, f := range all {
			names[i], sizes[i] = f.Name, f.Size
		}
		prog := ui.StartProgress(names, sizes, p.env.Interactive, func(s string) { p.env.printf("%s\n", s) })
		up := upload.New(p.c)
		var wg sync.WaitGroup
		errs := make([]error, len(all))
		sem := make(chan struct{}, 2)
		for i, f := range all {
			wg.Add(1)
			go func(i int, f media.File) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				// Already stored (same fingerprint): reuse it instead of uploading again.
				if d, ok := p.dups[f.SHA256]; ok && d.StorageStatus == "STORED" && d.AssetID != "" {
					if m, err := p.c.Media(ctx, d.AssetID); err == nil && m.StorageStatus == "STORED" {
						items[i] = compose.Item{File: f, Media: *m}
						prog.Finish(i, "reused")
						return
					}
				}
				m, err := up.Upload(ctx, f, p.plan.Project.ID, func(sent, _ int64) { prog.Update(i, sent) })
				if err != nil {
					errs[i] = err
					prog.Finish(i, "failed")
					return
				}
				items[i] = compose.Item{File: f, Media: *m}
				prog.Finish(i, "done")
			}(i, f)
		}
		wg.Wait()
		prog.Done()
		if err := errors.Join(errs...); err != nil {
			return err
		}
	}
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
	if n < 2 && len(p.plan.PerAccount) == 0 {
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
	}
	verdict := func(c int, ticked []int) ui.Verdict {
		order := orderFor(own[c], ticked)
		ms := make([]api.Media, len(order))
		for i, r := range order {
			ms[i] = p.plan.Items[r].Media
		}
		f := compose.AutoFormat(accs[c].Platform, ms)
		if f == "" {
			return ui.Verdict{Label: "can't post", OK: false}
		}
		return ui.Verdict{Label: fmt.Sprintf("%s (%d)", compose.FormatLabel[f], len(order)), OK: true}
	}
	for {
		col, err := ui.Grid(rows, cols, sel, verdict)
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
		if err != nil && !errors.Is(err, ui.ErrCancelled) {
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

func (p *poster) details(ctx context.Context, d *media.Defaults) error {
	plan := p.plan
	caption := p.f.caption
	if p.f.captionFile != "" {
		data, err := os.ReadFile(p.f.captionFile)
		if err != nil {
			return err
		}
		caption = strings.TrimSpace(string(data))
	}
	if caption == "" {
		caption = d.Caption
	}
	if caption == "" && len(p.files) > 0 {
		caption = media.SidecarCaption(p.files[0])
	}
	hashtags := p.f.hashtags
	if hashtags == "" {
		hashtags = strings.Join(d.Hashtags, " ")
	}
	link := firstNonEmpty(p.f.link, d.Link)
	firstComment := firstNonEmpty(p.f.firstComm, d.FirstComment)
	ai := p.f.ai || (d.MadeWithAI != nil && *d.MadeWithAI)
	plan.InternalTitle = p.f.title
	needsComment := slices.ContainsFunc(plan.Accounts, func(a api.ProjectAccount) bool { return compose.CommentsOn(a.Platform, plan.FormatFor(a)) })

	if p.wizard {
		fields := []huh.Field{
			huh.NewInput().Title("Internal name (only you see it)").Placeholder("optional").Value(&plan.InternalTitle),
			huh.NewText().Title("Caption").Description("ctrl+e opens your editor").Value(&caption).CharLimit(63000).Lines(5).ExternalEditor(true),
			huh.NewInput().Title("Hashtags").Placeholder("android kotlin #productivity").Value(&hashtags),
			huh.NewInput().Title("Link").Placeholder("https://… (optional)").Value(&link),
		}
		if needsComment {
			fields = append(fields, huh.NewInput().Title("First comment").Description("Facebook Pages and Instagram, right after the post goes live").Value(&firstComment))
		}
		fields = append(fields, huh.NewConfirm().Title("Made with AI?").Description("Adds each platform's own AI label (none exists on Facebook)").Value(&ai))
		if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
			return err
		}
	}
	plan.Caption, plan.Link, plan.FirstComment, plan.MadeWithAI = caption, link, firstComment, ai
	plan.Hashtags = compose.ParseHashtags(hashtags)
	for _, a := range mustMatch(splitList(p.f.aiOff), plan.Accounts) {
		plan.AIOverride[a.AccountID] = false
	}
	if p.wizard && ai && len(plan.Accounts) > 1 {
		var opts []huh.Option[string]
		for _, a := range plan.Accounts {
			if compose.AILabelSupported(a.Platform, false) {
				opts = append(opts, huh.NewOption(accountLabel(a), a.AccountID).Selected(true))
			}
		}
		if len(opts) > 1 {
			var on []string
			if err := huh.NewMultiSelect[string]().Title("AI label on").Options(opts...).Value(&on).Run(); err != nil {
				return err
			}
			for _, o := range opts {
				if !slices.Contains(on, o.Value) {
					plan.AIOverride[o.Value] = false
				}
			}
		}
	}
	if p.wizard && len(plan.Accounts) > 1 {
		different := false
		if err := huh.NewConfirm().Title("A different caption for some accounts?").Value(&different).Run(); err != nil {
			return err
		}
		if different {
			var ids []string
			opts := make([]huh.Option[string], len(plan.Accounts))
			for i, a := range plan.Accounts {
				opts[i] = huh.NewOption(accountLabel(a), a.AccountID)
			}
			if err := huh.NewMultiSelect[string]().Title("Which accounts").Options(opts...).Value(&ids).Run(); err != nil {
				return err
			}
			for _, a := range plan.Accounts {
				if slices.Contains(ids, a.AccountID) {
					c := caption
					if err := huh.NewText().Title("Caption for " + accountLabel(a)).Value(&c).Lines(5).ExternalEditor(true).Run(); err != nil {
						return err
					}
					plan.Captions[a.AccountID] = c
				}
			}
		}
	}
	for _, a := range plan.Accounts {
		var err error
		switch a.Platform {
		case "YOUTUBE":
			err = p.youtube(a)
		case "PINTEREST":
			err = p.pinterest(ctx, a)
		case "TIKTOK":
			err = p.tiktokSettings(ctx, a)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *poster) youtube(a api.ProjectAccount) error {
	title := firstNonEmpty(p.f.title, p.plan.InternalTitle, firstLine(p.plan.Caption))
	if len([]rune(title)) > 100 {
		title = string([]rune(title)[:100])
	}
	privacy := p.f.youtubePrivacy
	if privacy == "public" {
		privacy = ""
	}
	if p.wizard {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("YouTube title · "+a.DisplayName).CharLimit(100).Value(&title).Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("YouTube needs a title")
				}
				return nil
			}),
			huh.NewSelect[string]().Title("Visibility").Options(huh.NewOption("Public", ""), huh.NewOption("Unlisted", "unlisted"), huh.NewOption("Private", "private")).Value(&privacy),
		)).Run(); err != nil {
			return err
		}
	}
	p.plan.Titles[a.AccountID] = title
	p.plan.YouTubePrivacy[a.AccountID] = privacy
	return nil
}

func (p *poster) pinterest(ctx context.Context, a api.ProjectAccount) error {
	board := p.f.board
	title := firstNonEmpty(p.f.title, p.plan.InternalTitle)
	pinLink := ""
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
		if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
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
	info, err := p.c.TikTokCreatorInfo(ctx, a.AccountID)
	if err != nil {
		return fmt.Errorf("TikTok %s: %w", a.DisplayName, err)
	}
	p.tiktok[a.AccountID] = info
	c := compose.TikTokChoice{Privacy: p.f.tiktokPrivacy, Draft: p.f.tiktokDraft}
	for _, x := range splitList(p.f.tiktokAllow) {
		switch strings.ToLower(x) {
		case "comments", "comment":
			c.AllowComments = true
		case "duet":
			c.AllowDuet = true
		case "stitch":
			c.AllowStitch = true
		}
	}
	format := p.plan.FormatFor(a)
	if p.wizard {
		p.env.printf("\n%s %s (@%s)\n", bold.Render("TikTok:"), info.Nickname, info.Username)
		mode := "direct"
		if err := huh.NewSelect[string]().Title("How to send it to TikTok").Options(
			huh.NewOption("Post directly", "direct"),
			huh.NewOption("Send to my TikTok inbox as a draft (I finish it in the app)", "draft")).Value(&mode).Run(); err != nil {
			return err
		}
		c.Draft = mode == "draft"
		if !c.Draft {
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
			var allow []string
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
			fields := []huh.Field{huh.NewSelect[string]().Title("Who can view this post").Options(opts...).Value(&c.Privacy).Validate(func(v string) error {
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
			if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
				return err
			}
			c.AllowComments, c.AllowDuet, c.AllowStitch = slices.Contains(allow, "comments"), slices.Contains(allow, "duet"), slices.Contains(allow, "stitch")
			if c.BrandDisclose {
				var kinds []string
				if err := huh.NewMultiSelect[string]().Title("What kind").Options(
					huh.NewOption("Your brand (you promote yourself or your business)", "organic"),
					huh.NewOption("Branded content (a paid partnership with a third party)", "content")).Value(&kinds).Validate(func(v []string) error {
					if len(v) == 0 {
						return errors.New(`choose "Your brand", "Branded content" or both`)
					}
					return nil
				}).Run(); err != nil {
					return err
				}
				c.BrandOrganic, c.BrandContent = slices.Contains(kinds, "organic"), slices.Contains(kinds, "content")
			}
			if label := compose.TikTokLabel(c); label != "" {
				p.env.printf("  Your post will be labeled %q.\n", label)
			}
		}
		if format == "PHOTO_POST" && !c.Draft {
			t := firstNonEmpty(p.plan.InternalTitle)
			if err := huh.NewInput().Title("TikTok photo title").CharLimit(90).Value(&t).Run(); err != nil {
				return err
			}
			p.plan.Titles[a.AccountID] = t
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

// ------------------------------------------------------------------ when

func (p *poster) chooseWhen(ctx context.Context, d *media.Defaults) error {
	when := strings.ToLower(strings.TrimSpace(firstNonEmpty(p.f.when, d.When, "draft")))
	if p.wizard {
		opts := []huh.Option[string]{huh.NewOption("Save as a draft (finish on the website)", "draft")}
		if p.me.Allows("SCHEDULE") {
			opts = append(opts, huh.NewOption("Next free posting slot", "next-slot"), huh.NewOption("At a time I choose", "at"))
		}
		if p.me.Allows("PUBLISH") {
			opts = append(opts, huh.NewOption("Publish now", "now"))
		}
		if p.me.RequiresApproval && p.me.Allows("SCHEDULE") {
			p.env.printf("%s\n", faint.Render("Scheduling and publishing go to an admin for approval first."))
		}
		when = "draft"
		if err := huh.NewSelect[string]().Title("When").Options(opts...).Value(&when).Run(); err != nil {
			return err
		}
		if when == "at" {
			at := time.Now().Add(time.Hour).Format("2006-01-02 15:00")
			if err := huh.NewInput().Title("Date and time ("+p.plan.Project.Timezone+")").Description("YYYY-MM-DD HH:MM").Value(&at).
				Validate(func(s string) error {
					local, err := compose.LocalDateTime(s)
					if err != nil {
						return err
					}
					if !compose.InFuture(local, p.plan.Project.Timezone, time.Now()) {
						return errors.New("that time has passed")
					}
					return nil
				}).Run(); err != nil {
				return err
			}
			when = at
		}
	}
	switch when {
	case "draft", "":
		p.plan.When = compose.When{Kind: "DRAFT"}
	case "now":
		if !p.me.Allows("PUBLISH") {
			return errors.New("this terminal may not publish now (create a login code that allows it, or schedule instead)")
		}
		p.plan.When = compose.When{Kind: "NOW"}
	case "next-slot":
		if !p.me.Allows("SCHEDULE") {
			return errors.New("this terminal may only save drafts")
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
		p.env.printf("Next free slot: %s (%s)\n", bold.Render(p.plan.When.At), p.plan.Project.Timezone)
	default:
		if !p.me.Allows("SCHEDULE") {
			return errors.New("this terminal may only save drafts")
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
		e.printf("%s Nothing was uploaded or saved. This is the post that would be sent:\n", yellow.Render("Dry run:"))
		enc := json.NewEncoder(os.Stdout)
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
	tiktokDirect, branded := false, false
	for _, a := range p.plan.Accounts {
		if c, ok := p.plan.TikTok[a.AccountID]; ok && !c.Draft {
			tiktokDirect = true
			branded = branded || c.BrandContent
		}
	}
	if v.ErrorCount > 0 {
		e.printf("\n%s It's saved as a draft: fix it on the website (mktauto open %s) or change the files and try again.\n", red.Render("Can't publish yet."), post.ID)
		p.remember(post, "draft")
		return errSilent
	}
	action := map[string]string{"DRAFT": "Save the draft", "AT": "Schedule it for " + p.plan.When.At + " (" + p.plan.Project.Timezone + ")", "NOW": "Publish now"}[p.plan.When.Kind]
	if p.plan.When.Kind != "DRAFT" && tiktokDirect {
		e.printf("\nBy posting to TikTok, you agree to TikTok's Music Usage Confirmation (%s)", faint.Render(compose.TikTokMusicUsageURL))
		if branded {
			e.printf(" and Branded Content Policy (%s)", faint.Render(compose.TikTokBrandedPolicyURL))
		}
		e.printf(".\n")
	}
	if p.wizard && !p.f.yes && p.plan.When.Kind != "DRAFT" {
		ok := true
		if err := huh.NewConfirm().Title(action+"?").Affirmative("Yes").Negative("No, keep it as a draft").Value(&ok).Run(); err != nil {
			return err
		}
		if !ok {
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
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	return nil
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

