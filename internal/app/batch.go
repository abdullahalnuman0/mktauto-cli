package app

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
	"github.com/charmbracelet/huh"
)

// Many posts at once (ADR-077): `mktauto batch` makes one post per file or subfolder; `mktauto import` one per row
// of a spreadsheet. Both show the whole plan first; each post then goes through the same steps as `mktauto post`.

// batchItem is one planned post.
type batchItem struct {
	dir   string // the folder its files are in
	files []media.File
	f     postFlags // everything else, as `mktauto post` flags
	note  string    // why it's left out ("" = it's posted)
}

func (it batchItem) label() string {
	if len(it.files) == 0 {
		return "(text)"
	}
	names := make([]string, len(it.files))
	for i, f := range it.files {
		names[i] = f.Name
	}
	return strings.Join(names, ", ")
}

// ------------------------------------------------------------------ mktauto batch

func (e *Env) batch(ctx context.Context, args []string) error {
	var f postFlags
	fs := newPostFlags(&f)
	fs.SetOutput(e.Err)
	each := fs.String("each", "", "one post per: image, video, file (image or video) or folder (a subfolder's files together)")
	every := fs.String("every", "", "with --when \"YYYY-MM-DD HH:MM\": the time between posts, e.g. 1d, 12h, 2d6h")
	includePosted := fs.Bool("include-posted", false, "also files already posted from this folder")
	limit := fs.Int("limit", 0, "at most this many posts")
	fs.Usage = func() { fmt.Fprint(e.Err, batchHelp) }
	args, err := expandPreset(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	me, err := e.me(ctx, c)
	if err != nil {
		return err
	}
	b := &batcher{env: e, c: c, me: me, ask: newAsker(), f: f, each: *each, every: *every, includePosted: *includePosted, limit: *limit}
	b.interactive = e.Interactive && f.project == "" && f.accounts == "" && *each == ""
	return b.run(ctx)
}

type batcher struct {
	env                      *Env
	c                        *api.Client
	me                       *api.Me
	ask                      *asker
	f                        postFlags
	each, every              string
	includePosted            bool
	limit                    int
	interactive              bool
	watch                    bool // mktauto watch: one post per new file, as it arrives
	project                  api.Project
	accounts                 []api.ProjectAccount
	chosen                   []string // account ids
	whenMode, start, caption string
	items                    []batchItem
}

func (b *batcher) run(ctx context.Context) error {
	e := b.env
	projects, err := b.c.Projects(ctx)
	if err != nil {
		return err
	}
	var live []api.Project
	for _, p := range projects {
		if !p.Archived {
			live = append(live, p)
		}
	}
	steps := []step{
		{"what", b.chooseEach},
		{"project", func() error { return b.chooseProject(ctx, live) }},
		{"accounts", func() error { return b.chooseAccounts(ctx) }},
		{"when", b.chooseWhen},
		{"plan", func() error { return b.plan(ctx) }},
	}
	if err := runSteps(b.ask, steps, false); errors.Is(err, errNothing) {
		return nil
	} else if err != nil {
		return err
	}
	var todo []batchItem
	for _, it := range b.items {
		if it.note == "" {
			todo = append(todo, it)
		}
	}
	if b.f.dryRun {
		e.printf("%s Nothing was uploaded or saved.\n", yellow.Render("Dry run:"))
		return nil
	}
	return runItems(ctx, e, todo)
}

func (b *batcher) chooseEach() error {
	if !b.interactive {
		if b.each == "" {
			b.each = "file"
		}
		if b.watch && b.each == "folder" {
			return withCode(exitUsage, errors.New("--each for watch: image, video or file"))
		}
		if !slices.Contains([]string{"image", "video", "file", "folder"}, b.each) {
			return fmt.Errorf("--each %q: use image, video, file or folder", b.each)
		}
		return nil
	}
	all, _ := media.Scan(b.env.Dir)
	images, videos := countKinds(all)
	folders := len(subfolders(b.env.Dir))
	var opts []huh.Option[string]
	if images > 0 {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Each image as its own post  (%d)", images), "image"))
	}
	if videos > 0 {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Each video as its own post: Reels, Shorts, TikToks  (%d)", videos), "video"))
	}
	if images > 0 && videos > 0 {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Each image and video  (%d)", images+videos), "file"))
	}
	if folders > 0 && !b.watch {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Each subfolder as one post: its files together, caption.txt as the caption  (%d)", folders), "folder"))
	}
	if len(opts) == 0 && b.watch {
		opts = []huh.Option[string]{huh.NewOption("Each new image or video", "file"), huh.NewOption("Each new image", "image"), huh.NewOption("Each new video", "video")}
	}
	if len(opts) == 0 {
		return errors.New("nothing here to post: no images, videos or subfolders with them")
	}
	if b.each == "" {
		b.each = opts[0].Value
	}
	return b.ask.form(huh.NewSelect[string]().Title("Post many at once").
		Description("each post gets its own caption from a .txt next to the file (reel.txt for reel.mp4), else the one you give").
		Options(opts...).Value(&b.each))
}

func subfolders(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, en := range entries {
		if en.IsDir() && !strings.HasPrefix(en.Name(), ".") {
			if files, _ := media.Scan(filepath.Join(dir, en.Name())); len(files) > 0 {
				out = append(out, filepath.Join(dir, en.Name()))
			}
		}
	}
	return out
}

func (b *batcher) chooseProject(ctx context.Context, live []api.Project) error {
	if len(live) == 0 {
		return errors.New("no projects available to this terminal")
	}
	if !b.interactive || len(live) == 1 {
		term := firstNonEmpty(b.f.project, b.env.Config.LastProject[b.env.Dir])
		if len(live) == 1 && term == "" {
			term = live[0].ID
		}
		p, err := findProject(live, term)
		if err != nil {
			return fmt.Errorf("choose a project with --project: %w", err)
		}
		b.project = *p
	} else {
		id := firstNonEmpty(b.project.ID, b.env.Config.LastProject[b.env.Dir])
		opts := make([]huh.Option[string], len(live))
		for i, p := range live {
			opts[i] = huh.NewOption(p.Name, p.ID)
		}
		if err := b.ask.form(huh.NewSelect[string]().Title("Project").Options(opts...).Value(&id)); err != nil {
			return err
		}
		p, _ := findProject(live, id)
		b.project = *p
	}
	accs, err := b.c.ProjectAccounts(ctx, b.project.ID)
	if err != nil {
		return err
	}
	slices.SortFunc(accs, func(x, y api.ProjectAccount) int { return x.SortOrder - y.SortOrder })
	b.accounts = accs
	return nil
}

func (b *batcher) chooseAccounts(ctx context.Context) error {
	if !b.interactive {
		if b.f.accounts == "" {
			return errors.New("choose accounts with --accounts")
		}
		return nil
	}
	var opts []huh.Option[string]
	for _, a := range b.accounts {
		if a.Usable() {
			sel := slices.Contains(b.chosen, a.AccountID) || (b.chosen == nil && a.DefaultSelected)
			opts = append(opts, huh.NewOption(accountLabel(a), a.AccountID).Selected(sel))
		}
	}
	if len(opts) == 0 {
		return errors.New("none of this project's accounts can post right now")
	}
	ids := slices.Clone(b.chosen)
	if err := b.ask.form(huh.NewMultiSelect[string]().Title("Accounts for every post").
		Description("an account that can't take a post (e.g. YouTube and an image) is left out of that one").Options(opts...).Value(&ids).
		Validate(func(v []string) error {
			if len(v) == 0 {
				return errors.New("tick at least one account")
			}
			return nil
		})); err != nil {
		return err
	}
	b.chosen = ids
	if slices.ContainsFunc(b.accounts, func(a api.ProjectAccount) bool { return a.Platform == "TIKTOK" && slices.Contains(ids, a.AccountID) }) &&
		b.f.tiktokPrivacy == "" {
		b.f.tiktokPrivacy = compose.TikTokSelfOnly
		var opts []huh.Option[string]
		for _, lvl := range []string{"PUBLIC_TO_EVERYONE", "MUTUAL_FOLLOW_FRIENDS", "FOLLOWER_OF_CREATOR", compose.TikTokSelfOnly} {
			opts = append(opts, huh.NewOption(compose.TikTokPrivacyLabel[lvl], lvl))
		}
		if err := b.ask.form(huh.NewSelect[string]().Title("TikTok: who can view these posts").
			Description("comments, duet and stitch stay off; change single posts on the website").Options(opts...).Value(&b.f.tiktokPrivacy)); err != nil {
			return err
		}
	}
	return nil
}

func (b *batcher) chooseWhen() error {
	if b.watch {
		return b.chooseWatchWhen()
	}
	if !b.interactive {
		b.whenMode = strings.ToLower(firstNonEmpty(b.f.when, "draft"))
		if b.whenMode == "now" {
			return errors.New("--when now isn't for many posts at once: use next-slot (the project's posting times) or a start time with --every")
		}
		return nil
	}
	var opts []huh.Option[string]
	if b.me.Allows("SCHEDULE") {
		opts = append(opts, huh.NewOption("In the project's next free posting times", "next-slot"),
			huh.NewOption("From a start time, one every …", "every"))
	}
	opts = append(opts, huh.NewOption("As drafts (schedule them on the website)", "draft"))
	if b.whenMode == "" {
		b.whenMode = opts[0].Value
	}
	if b.start == "" {
		b.start = time.Now().Add(24 * time.Hour).Format("2006-01-02 10:00")
		b.every = firstNonEmpty(b.every, "1d")
	}
	return runSteps(b.ask, []step{
		{run: func() error {
			return b.ask.form(huh.NewSelect[string]().Title("When").Options(opts...).Value(&b.whenMode))
		}},
		{run: func() error {
			if b.whenMode != "every" {
				return nil
			}
			return b.ask.form(
				huh.NewInput().Title("First post ("+b.project.Timezone+")").Description("YYYY-MM-DD HH:MM").Value(&b.start).Validate(func(s string) error {
					local, err := compose.LocalDateTime(s)
					if err == nil && !compose.InFuture(local, b.project.Timezone, time.Now()) {
						err = errors.New("that time has passed")
					}
					return err
				}),
				huh.NewInput().Title("Then one every").Description("e.g. 1d, 12h, 2d6h").Value(&b.every).Validate(func(s string) error {
					_, err := parseEvery(s)
					return err
				}))
		}},
		{run: func() error {
			if b.caption == "" {
				b.caption = b.f.caption
			}
			return b.ask.form(huh.NewText().Title("Caption for posts without their own .txt").Description("optional · {name} = the file or folder name").
				Lines(3).Value(&b.caption))
		}},
	}, false)
}

// parseEvery: "1d", "12h", "2d6h", "90m".
func parseEvery(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	var total time.Duration
	if days, rest, ok := strings.Cut(s, "d"); ok {
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil || n < 0 {
			return 0, fmt.Errorf("%q: write e.g. 1d, 12h or 2d6h", s)
		}
		total, s = time.Duration(n)*24*time.Hour, rest
	}
	if s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("%q: write e.g. 1d, 12h or 2d6h", s)
		}
		total += d
	}
	if total < 10*time.Minute {
		return 0, errors.New("at least 10 minutes between posts")
	}
	return total, nil
}

// plan builds the posts, gives them times and shows the whole plan to confirm.
func (b *batcher) plan(ctx context.Context) error {
	e := b.env
	b.items = nil
	posted := postedHere(e.Dir)
	add := func(dir string, files []media.File, name string) {
		b.items = append(b.items, b.makeItem(posted, dir, files, name))
	}
	all, _ := media.Scan(e.Dir)
	media.ProbeAll(all)
	switch b.each {
	case "folder":
		for _, dir := range subfolders(e.Dir) {
			files, _ := media.Scan(dir)
			media.ProbeAll(files)
			add(dir, files, filepath.Base(dir))
		}
	default:
		for _, f := range all {
			if (b.each == "image" && f.IsVideo()) || (b.each == "video" && !f.IsVideo()) {
				continue
			}
			add(e.Dir, []media.File{f}, strings.TrimSuffix(f.Name, filepath.Ext(f.Name)))
		}
	}
	if b.limit > 0 {
		n := 0
		for i := range b.items {
			if b.items[i].note == "" {
				if n++; n > b.limit {
					b.items[i].note = "over --limit"
				}
			}
		}
	}
	if err := checkAccounts(b.accounts, b.items); err != nil {
		return err
	}
	if err := assignTimes(ctx, b.c, b.me, b.project, b.items, b.whenMode, b.start, b.every); err != nil {
		return err
	}
	return confirmPlan(e, b.ask, b.project, b.items, b.f.yes || b.f.dryRun)
}

// makeItem: one post for these files, with the shared settings (its own .txt wins over the shared caption).
func (b *batcher) makeItem(posted map[string]string, dir string, files []media.File, name string) batchItem {
	it := batchItem{dir: dir, files: files, f: b.f}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
		if posted[f.Name] == "posted" && !b.includePosted && dir == b.env.Dir {
			it.note = "posted before (--include-posted to post it again)"
		}
	}
	it.f.files = strings.Join(names, ",")
	it.f.project = b.project.ID
	if b.interactive {
		it.f.accounts = strings.Join(b.chosen, ",")
	}
	if c := media.SidecarCaption(files[0]); c != "" && it.f.captionFile == "" {
		it.f.caption = c
	} else if c := firstNonEmpty(b.caption, b.f.caption); c != "" {
		it.f.caption = strings.ReplaceAll(c, "{name}", name)
	}
	return it
}

// checkAccounts: per post, the accounts that can take its files (the others are left out of that post).
func checkAccounts(accounts []api.ProjectAccount, items []batchItem) error {
	for i := range items {
		it := &items[i]
		if it.note != "" {
			continue
		}
		matched, err := compose.MatchAccounts(splitList(it.f.accounts), accounts)
		if err != nil {
			return fmt.Errorf("%s: %w", it.label(), err)
		}
		ms := localMedia(it.files)
		var ok []string
		for _, a := range matched {
			if _, why := compose.Fit(a.Platform, ms); why == "" && a.Usable() {
				ok = append(ok, a.AccountID)
			}
		}
		if len(ok) == 0 {
			it.note = "none of the accounts can post this"
			continue
		}
		it.f.accounts = strings.Join(ok, ",")
	}
	return nil
}

// assignTimes gives each post its time: the next free posting slots, a start time and an interval, or drafts.
func assignTimes(ctx context.Context, c *api.Client, me *api.Me, project api.Project, items []batchItem, mode, start, every string) error {
	var todo []*batchItem
	for i := range items {
		if items[i].note == "" {
			todo = append(todo, &items[i])
		}
	}
	if len(todo) == 0 {
		return nil
	}
	switch mode {
	case "draft", "":
		for _, it := range todo {
			it.f.when = "draft"
		}
		return nil
	case "next-slot":
		if !me.Allows("SCHEDULE") {
			return withCode(exitNotAllowed, errors.New("this terminal may only save drafts"))
		}
		slots, err := c.NextSlots(ctx, project.ID, min(len(todo), 50))
		if err != nil {
			return serverRefused(err)
		}
		if len(slots) < len(todo) {
			return fmt.Errorf("the project has %d free posting times in the next 8 weeks for %d posts: add posting times on the website, or use --limit", len(slots), len(todo))
		}
		for i, it := range todo {
			it.f.when = strings.Replace(slots[i].LocalDateTime[:16], "T", " ", 1)
		}
		return nil
	}
	if !me.Allows("SCHEDULE") {
		return withCode(exitNotAllowed, errors.New("this terminal may only save drafts"))
	}
	if mode != "every" {
		start = mode
	}
	local, err := compose.LocalDateTime(start)
	if err != nil {
		return err
	}
	if !compose.InFuture(local, project.Timezone, time.Now()) {
		return fmt.Errorf("%s has passed in %s", start, project.Timezone)
	}
	var step time.Duration
	if len(todo) > 1 {
		if every == "" {
			return errors.New("several posts from one start time: add --every (e.g. 1d or 12h)")
		}
		if step, err = parseEvery(every); err != nil {
			return err
		}
	}
	t, _ := time.Parse("2006-01-02T15:04:05", local)
	for _, it := range todo {
		it.f.when = t.Format("2006-01-02 15:04")
		t = t.Add(step)
	}
	return nil
}

// errNothing: every file was left out (already posted, over --limit…); not a failure.
var errNothing = errors.New("nothing to post")

// confirmPlan prints the plan and asks to go ahead (Esc goes back).
func confirmPlan(e *Env, ask *asker, project api.Project, items []batchItem, yes bool) error {
	n := 0
	var b strings.Builder
	fmt.Fprintf(&b, "%-3s %-34s %-18s %-9s %s\n", "#", "Files", "When ("+project.Timezone+")", "Accounts", "Caption")
	for _, it := range items {
		if it.note != "" {
			fmt.Fprintf(&b, "%s\n", faint.Render(fmt.Sprintf("-   %-34s %s", truncate(it.label(), 34), it.note)))
			continue
		}
		n++
		when := it.f.when
		fmt.Fprintf(&b, "%-3d %-34s %-18s %-9d %s\n", n, truncate(it.label(), 34), when, len(splitList(it.f.accounts)),
			faint.Render(truncate(strings.ReplaceAll(firstNonEmpty(it.f.caption, "(no caption)"), "\n", " "), 40)))
	}
	if n == 0 {
		e.printf("%s\n%s", bold.Render("Nothing new to post"), b.String())
		return errNothing
	}
	summary := fmt.Sprintf("%s to %s", plural(n, "post"), project.Name)
	if yes || !e.Interactive {
		e.printf("%s\n%s\n", bold.Render("Plan: "+summary), b.String())
		return nil
	}
	ok := "go"
	return ask.form(huh.NewNote().Title("Plan: "+summary).Description(strings.TrimRight(b.String(), "\n")),
		huh.NewSelect[string]().Title("Ready?").Description("esc goes back to change anything · each post is uploaded and saved in turn").
			Options(huh.NewOption(fmt.Sprintf("Create the %s", plural(n, "post")), "go")).Value(&ok))
}

// runItems creates the posts one by one through the same steps as `mktauto post`, with one line each.
func runItems(ctx context.Context, e *Env, items []batchItem) error {
	failed := 0
	for i, it := range items {
		sub := *e
		sub.Dir, sub.Out, sub.Interactive = it.dir, io.Discard, false
		f := it.f
		f.yes, f.jsonOut, f.dryRun = true, false, false
		if f.duplicates == "" {
			f.duplicates = "new-only"
		}
		p := &poster{env: &sub, f: f, ask: newAsker()}
		start := time.Now()
		err := p.run(ctx)
		prefix := fmt.Sprintf("%d/%d %s", i+1, len(items), truncate(it.label(), 40))
		if err != nil {
			failed++
			e.printf("%s %s: %v\n", red.Render("✗"), prefix, err)
			if errors.Is(err, context.Canceled) {
				return err
			}
			continue
		}
		what := "draft"
		if p.result != nil && p.plan.When.Kind == "AT" {
			what = "scheduled " + p.plan.When.At
			if p.result.Status == "PENDING_APPROVAL" {
				what += " (waits for approval)"
			}
		}
		id := ""
		if p.result != nil {
			id = p.result.ID[:min(8, len(p.result.ID))]
		}
		e.printf("%s %s → %s · %s %s\n", green.Render("✓"), prefix, what, plural(len(p.plan.Accounts), "account"),
			faint.Render(id+" · "+time.Since(start).Round(100*time.Millisecond).String()))
	}
	if failed > 0 {
		e.printf("%s\n", yellow.Render(fmt.Sprintf("%d of %d failed; the others are saved. Fix and run again: posted files are skipped.", failed, len(items))))
		return errSilent
	}
	e.printf("%s\n", faint.Render("See them: mktauto posts · mktauto status"))
	return nil
}

// ------------------------------------------------------------------ mktauto import

const importExample = `files,caption,hashtags,accounts,when,title,link,first_comment
launch.png;shot2.png;shot3.png,"Version 2 is out!",android kotlin,facebook;instagram,2026-12-01 18:30,,https://example.com,Download link in bio
reel.mp4,"60 seconds of what's new",reels,instagram;tiktok;youtube,next-slot,What's new in v2,,
,"A text-only update for Facebook and X",,facebook;x,draft,,,
`

func (e *Env) importCSV(ctx context.Context, args []string) error {
	var f postFlags
	fs := newPostFlags(&f)
	fs.SetOutput(e.Err)
	example := fs.Bool("example", false, "print an example file (mktauto import --example > plan.csv)")
	fs.Usage = func() {
		fmt.Fprint(e.Err, `Usage: mktauto import FILE.csv [--project NAME] [--dry-run] [--yes] [any mktauto post flag as the default for every row]
One post per row. Columns (first row = names, any order): files (separated by ;), caption or caption_file, hashtags,
accounts (separated by ;), when (YYYY-MM-DD HH:MM, next-slot or draft), title, name, link, first_comment, template,
tiktok_privacy, youtube_privacy, project. Empty cells use the flags. File paths are relative to the CSV's folder.
mktauto import --example prints an example.
`)
	}
	var file string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		file, args = args[0], args[1:]
	}
	args, err := expandPreset(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *example {
		fmt.Fprint(e.Out, importExample)
		return nil
	}
	file = firstNonEmpty(file, fs.Arg(0))
	if file == "" {
		fs.Usage()
		return flag.ErrHelp
	}
	rows, err := readCSV(file)
	if err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	me, err := e.me(ctx, c)
	if err != nil {
		return err
	}
	projects, err := c.Projects(ctx)
	if err != nil {
		return err
	}
	dir, _ := filepath.Abs(filepath.Dir(file))
	byProject := map[string][]batchItem{}
	var order []string
	var problems []string
	for n, row := range rows {
		line := n + 2
		it := batchItem{dir: dir, f: f}
		set := func(col string, dst *string) {
			if v := strings.TrimSpace(row[col]); v != "" {
				*dst = v
			}
		}
		set("caption", &it.f.caption)
		set("hashtags", &it.f.hashtags)
		set("title", &it.f.title)
		set("name", &it.f.name)
		set("link", &it.f.link)
		set("first_comment", &it.f.firstComm)
		set("template", &it.f.template)
		set("tiktok_privacy", &it.f.tiktokPrivacy)
		set("youtube_privacy", &it.f.youtubePrivacy)
		set("when", &it.f.when)
		set("project", &it.f.project)
		if v := strings.TrimSpace(row["caption_file"]); v != "" {
			it.f.captionFile = filepath.Join(dir, v)
		}
		if v := strings.TrimSpace(row["accounts"]); v != "" {
			it.f.accounts = strings.ReplaceAll(v, ";", ",")
		}
		var names []string
		for _, name := range strings.FieldsFunc(row["files"], func(r rune) bool { return r == ';' || r == '|' }) {
			name = strings.TrimSpace(name)
			mf, ok := media.Describe(filepath.Join(dir, name))
			if !ok {
				problems = append(problems, fmt.Sprintf("row %d: %s isn't an image or video MKTAuto accepts (or doesn't exist)", line, name))
				continue
			}
			it.files = append(it.files, mf)
			names = append(names, name)
		}
		it.f.files = strings.Join(names, ",")
		if it.f.accounts == "" {
			problems = append(problems, fmt.Sprintf("row %d: no accounts (column accounts or --accounts)", line))
		}
		if strings.EqualFold(it.f.when, "now") {
			problems = append(problems, fmt.Sprintf("row %d: \"now\" isn't for imports: use a time, next-slot or draft", line))
		}
		p, err := findProject(projects, firstNonEmpty(it.f.project, e.Config.LastProject[e.Dir]))
		if err != nil {
			problems = append(problems, fmt.Sprintf("row %d: project: %v", line, err))
			continue
		}
		it.f.project = p.ID
		if _, ok := byProject[p.ID]; !ok {
			order = append(order, p.ID)
		}
		byProject[p.ID] = append(byProject[p.ID], it)
	}
	if len(problems) > 0 {
		for _, pr := range problems {
			e.printf("%s %s\n", red.Render("✗"), pr)
		}
		return errSilent
	}
	ask := newAsker()
	var all []batchItem
	for _, pid := range order {
		p, _ := findProject(projects, pid)
		accs, err := c.ProjectAccounts(ctx, pid)
		if err != nil {
			return err
		}
		items := byProject[pid]
		if err := checkAccounts(accs, items); err != nil {
			return err
		}
		// Rows with next-slot (or no time while --when next-slot) share the project's free posting times, in row order.
		var slotted []batchItem
		var idx []int
		for i, it := range items {
			if it.note == "" && strings.EqualFold(it.f.when, "next-slot") {
				slotted, idx = append(slotted, it), append(idx, i)
			}
		}
		if len(slotted) > 0 {
			if err := assignTimes(ctx, c, me, *p, slotted, "next-slot", "", ""); err != nil {
				return err
			}
			for k, i := range idx {
				items[i].f.when = slotted[k].f.when
			}
		}
		for i := range items {
			if items[i].f.when == "" {
				items[i].f.when = "draft"
			}
		}
		if err := confirmPlan(e, ask, *p, items, f.yes || f.dryRun); err != nil && !errors.Is(err, errNothing) {
			return err
		}
		all = append(all, items...)
	}
	if f.dryRun {
		e.printf("%s Nothing was uploaded or saved.\n", yellow.Render("Dry run:"))
		return nil
	}
	var todo []batchItem
	for _, it := range all {
		if it.note == "" {
			todo = append(todo, it)
		}
	}
	return runItems(ctx, e, todo)
}

// readCSV: rows as column name (lower case, spaces → _) → value. A UTF-8 BOM (Excel) is ignored.
func readCSV(path string) ([]map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(data), "\uFEFF")
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("%s: a first row of column names, then one row per post (mktauto import --example)", path)
	}
	head := make([]string, len(records[0]))
	known := map[string]bool{"files": true, "caption": true, "caption_file": true, "hashtags": true, "accounts": true, "when": true,
		"title": true, "name": true, "link": true, "first_comment": true, "template": true, "tiktok_privacy": true, "youtube_privacy": true, "project": true}
	for i, h := range records[0] {
		head[i] = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(h)), " ", "_")
		if !known[head[i]] {
			return nil, fmt.Errorf("%s: unknown column %q", path, h)
		}
	}
	var rows []map[string]string
	for _, rec := range records[1:] {
		if strings.TrimSpace(strings.Join(rec, "")) == "" {
			continue
		}
		row := map[string]string{}
		for i, v := range rec {
			if i < len(head) {
				row[head[i]] = v
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

const batchHelp = `Usage: mktauto batch [flags]
One post per file or subfolder of this folder, all planned at once; no flags in a terminal: step by step.
Each post's caption comes from its own .txt (reel.txt for reel.mp4, caption.txt in a subfolder), else --caption
({name} = the file or folder name). Files already posted from this folder are skipped.

  --each image|video|file|folder   what makes one post (default: file)
  --project NAME  --accounts A,B   where (accounts that can't take a post are left out of that one)
  --when next-slot                 the project's next free posting times (one each)
  --when "YYYY-MM-DD HH:MM" --every 1d   a start time, then one every interval (12h, 2d6h…)
  --when draft                     drafts (default)
  --include-posted                 also files posted before      --limit N   at most N posts
  --dry-run                        show the plan only            --yes       don't ask
  Any mktauto post flag works for every post: --caption, --hashtags, --hashtag-set, --template, --tiktok-privacy…

Examples
  mktauto batch --each video --project Memoshot --accounts instagram,tiktok,youtube --when next-slot --tiktok-privacy SELF_ONLY
  mktauto batch --each folder --project Memoshot --accounts facebook,instagram --when "2026-12-01 10:00" --every 2d
`
