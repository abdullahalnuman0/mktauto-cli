package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
	"github.com/charmbracelet/huh"
)

// mktauto watch (ADR-079): new images and videos dropped into this folder become posts by themselves (drafts, the
// next free posting time, or published at once), with the same setup and checks as mktauto batch.

func (e *Env) watch(ctx context.Context, args []string) error {
	var f postFlags
	fs := newPostFlags(&f)
	fs.SetOutput(e.Err)
	each := fs.String("each", "", "which new files: image, video or file (both; default)")
	interval := fs.Duration("interval", 10*time.Second, "how often to look for new files")
	existing := fs.Bool("existing", false, "also post the files already here that weren't posted yet")
	fs.Usage = func() { fmt.Fprint(e.Err, watchHelp) }
	args, err := expandPreset(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval < 2*time.Second {
		return withCode(exitUsage, errors.New("--interval: at least 2s"))
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	me, err := e.me(ctx, c)
	if err != nil {
		return err
	}
	b := &batcher{env: e, c: c, me: me, ask: newAsker(), f: f, each: *each, watch: true}
	b.interactive = e.Interactive && f.project == "" && f.accounts == ""
	projects, err := c.Projects(ctx)
	if err != nil {
		return err
	}
	if err := runSteps(b.ask, []step{
		{"what", b.chooseEach},
		{"project", func() error { return b.chooseProject(ctx, liveProjects(projects)) }},
		{"accounts", func() error { return b.chooseAccounts(ctx) }},
		{"when", b.chooseWhen},
	}, false); err != nil {
		return err
	}
	return b.loop(ctx, *interval, *existing)
}

func (b *batcher) chooseWatchWhen() error {
	if !b.interactive {
		b.whenMode = strings.ToLower(firstNonEmpty(b.f.when, "draft"))
		switch b.whenMode {
		case "draft", "next-slot":
		case "now":
			if !b.me.Allows("PUBLISH") {
				return withCode(exitNotAllowed, errors.New("this terminal may not publish now"))
			}
		default:
			return withCode(exitUsage, errors.New("--when for watch: draft, next-slot or now"))
		}
		return nil
	}
	opts := []huh.Option[string]{}
	if b.me.Allows("SCHEDULE") {
		opts = append(opts, huh.NewOption("In the project's next free posting time", "next-slot"))
	}
	opts = append(opts, huh.NewOption("As a draft (finish on the website)", "draft"))
	if b.me.Allows("PUBLISH") {
		opts = append(opts, huh.NewOption("Publish at once", "now"))
	}
	if b.whenMode == "" {
		b.whenMode = opts[0].Value
	}
	if b.caption == "" {
		b.caption = b.f.caption
	}
	return b.ask.form(
		huh.NewSelect[string]().Title("Each new file goes out").Options(opts...).Value(&b.whenMode),
		huh.NewText().Title("Caption for files without their own .txt").Description("optional · {name} = the file name").Lines(3).Value(&b.caption))
}

// loop looks for new files every interval; a file is posted once its size stopped changing (fully copied).
func (b *batcher) loop(ctx context.Context, interval time.Duration, existing bool) error {
	e := b.env
	wanted := func(f media.File) bool {
		return !((b.each == "image" && f.IsVideo()) || (b.each == "video" && !f.IsVideo()))
	}
	seen := map[string]bool{} // handled (posted, failed or there before)
	if !existing {
		files, _ := media.Scan(e.Dir)
		for _, f := range files {
			seen[f.Path] = true
		}
	}
	pending := map[string]int64{} // path → size at the last look
	what := map[string]string{"file": "images and videos", "image": "images", "video": "videos"}[firstNonEmpty(b.each, "file")]
	how := map[string]string{"draft": "a draft", "next-slot": "the next free posting time", "now": "published at once"}[b.whenMode]
	e.printf("%s %s in %s → %s, %s on %s\n%s\n", brand.Render("Watching"), what, shortPath(e.Dir), how,
		plural(len(splitList(firstNonEmpty(strings.Join(b.chosen, ","), b.f.accounts))), "account"), b.project.Name,
		faint.Render("Drop files here; each one becomes a post (its .txt is its caption). Ctrl+C stops."))
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		posted := postedHere(e.Dir)
		files, _ := media.Scan(e.Dir)
		var ready []media.File
		for _, f := range files {
			if seen[f.Path] || !wanted(f) {
				continue
			}
			if posted[f.Name] == "posted" && !b.includePosted {
				seen[f.Path] = true
				continue
			}
			st, err := os.Stat(f.Path)
			if err != nil {
				continue
			}
			if size, ok := pending[f.Path]; ok && size == st.Size() {
				ready = append(ready, f)
				delete(pending, f.Path)
			} else {
				pending[f.Path] = st.Size() // new or still being copied: look again next time
			}
		}
		slices.SortFunc(ready, func(x, y media.File) int { return strings.Compare(x.Name, y.Name) })
		for _, f := range ready {
			seen[f.Path] = true
			b.postOne(ctx, f)
		}
		select {
		case <-ctx.Done():
			e.printf("%s\n", faint.Render("Stopped watching."))
			return nil
		case <-tick.C:
		}
	}
}

func (b *batcher) postOne(ctx context.Context, f media.File) {
	e := b.env
	media.Probe(&f)
	items := []batchItem{b.makeItem(nil, e.Dir, []media.File{f}, strings.TrimSuffix(f.Name, extOf(f.Name)))}
	if err := checkAccounts(b.accounts, items); err != nil || items[0].note != "" {
		why := items[0].note
		if err != nil {
			why = err.Error()
		}
		e.printf("%s %s: %s\n", yellow.Render("!"), f.Name, why)
		return
	}
	switch b.whenMode {
	case "next-slot":
		if err := assignTimes(ctx, b.c, b.me, b.project, items, "next-slot", "", ""); err != nil {
			e.printf("%s %s: %v\n", red.Render("✗"), f.Name, err)
			return
		}
	case "now":
		items[0].f.when = "now"
	default:
		items[0].f.when = "draft"
	}
	e.printf("%s %s\n", faint.Render(time.Now().Format("15:04:05")), f.Name)
	_ = runItems(ctx, e, items)
}

func extOf(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[i:]
	}
	return ""
}

const watchHelp = `Usage: mktauto watch [flags]
Keeps running: every new image or video dropped into this folder becomes a post (once it's fully copied), with its
own .txt as the caption, else --caption ({name} = the file name). No flags in a terminal: step by step. Ctrl+C stops.

  --project NAME --accounts A,B    where (accounts that can't take a file are left out of its post)
  --when draft|next-slot|now       a draft (default), the next free posting time, or published at once
  --each image|video|file          which files (default: both)
  --existing                       also the files already here that weren't posted yet
  --interval 10s                   how often to look
  Any mktauto post flag works for every post: --hashtags, --hashtag-set, --template, --tiktok-privacy, --preset…

Example
  mktauto watch --project Memoshot --accounts instagram,tiktok --when next-slot --tiktok-privacy SELF_ONLY
`
