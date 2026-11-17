package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/charmbracelet/huh"
	"github.com/dustin/go-humanize"
)

// Calendar, results, inbox, repost and repeat (ADR-078).

// ------------------------------------------------------------------ mktauto calendar

func (e *Env) calendar(ctx context.Context, args []string) error {
	fs := e.flags("calendar", "calendar [--days N] [--project NAME] [--json]   (what's planned, day by day, with the free posting times)")
	days := fs.Int("days", 14, "how many days ahead")
	project := fs.String("project", "", "only this project (and show its free posting times)")
	asJSON := fs.Bool("json", false, "print JSON")
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
	projects, err := c.Projects(ctx)
	if err != nil {
		return err
	}
	var only *api.Project
	if *project != "" {
		if only, err = findProject(projects, *project); err != nil {
			return err
		}
	} else if live := liveProjects(projects); len(live) == 1 {
		only = &live[0]
	}
	start := time.Now().Truncate(24 * time.Hour).Add(-time.Hour)
	end := time.Now().Add(time.Duration(*days) * 24 * time.Hour)
	all, err := c.PostsWhere(ctx, &start, "")
	if err != nil {
		return err
	}
	var posts []api.Post
	for _, p := range all {
		if p.ScheduledAt != nil && p.ScheduledAt.Before(end) && p.Status != "CANCELLED" && (only == nil || p.ProjectID == only.ID) {
			posts = append(posts, p)
		}
	}
	var free []api.NextSlot
	if only != nil && me.Allows("SCHEDULE") {
		if slots, err := c.NextSlots(ctx, only.ID, 30); err == nil {
			for _, s := range slots {
				if !s.ScheduledAt.IsZero() && s.ScheduledAt.Before(end) {
					free = append(free, s)
				}
			}
		}
	}
	if *asJSON {
		return e.printJSON(map[string]any{"posts": posts, "freeSlots": free})
	}
	names := map[string]string{}
	for _, p := range projects {
		names[p.ID] = p.Name
	}
	type entry struct {
		at   time.Time
		line string
	}
	byDay := map[string][]entry{}
	var dayKeys []string
	add := func(t time.Time, line string) {
		k := t.Local().Format("2006-01-02")
		if _, ok := byDay[k]; !ok {
			dayKeys = append(dayKeys, k)
		}
		byDay[k] = append(byDay[k], entry{t, line})
	}
	for _, p := range posts {
		var plats []string
		for _, d := range p.Destinations {
			if s := platformShort(d.Platform); !slices.Contains(plats, s) {
				plats = append(plats, s)
			}
		}
		proj := ""
		if only == nil {
			proj = faint.Render(" · " + names[p.ProjectID])
		}
		repeat := ""
		if p.RepeatEvery > 0 {
			repeat = faint.Render(fmt.Sprintf(" ↻ %dd", p.RepeatEvery))
		}
		add(*p.ScheduledAt, fmt.Sprintf("%s  %s %-40s %-14s %s%s%s", p.ScheduledAt.Local().Format("15:04"), dot(p.Status), truncate(p.Title(), 40),
			strings.Join(plats, " "), statusText(p.Status), repeat, proj)+faint.Render("  "+p.ID[:8]))
	}
	for _, s := range free {
		add(s.ScheduledAt, faint.Render(fmt.Sprintf("%s  ○ free posting time", s.ScheduledAt.Local().Format("15:04"))))
	}
	sort.Strings(dayKeys)
	if len(dayKeys) == 0 {
		e.printf("Nothing planned in the next %s.\n", plural(*days, "day"))
		if only != nil && me.Allows("SCHEDULE") {
			e.printf("%s\n", faint.Render("Fill it: mktauto batch --when next-slot"))
		}
		return nil
	}
	today := time.Now().Format("2006-01-02")
	for _, k := range dayKeys {
		d, _ := time.ParseInLocation("2006-01-02", k, time.Local)
		label := d.Format("Mon 2 Jan")
		if k == today {
			label += " · today"
		}
		e.printf("%s\n", bold.Render(label))
		list := byDay[k]
		sort.SliceStable(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
		for _, en := range list {
			e.printf("  %s\n", en.line)
		}
	}
	if len(free) > 0 {
		e.printf("%s\n", faint.Render(fmt.Sprintf("%s free · fill them: mktauto batch --when next-slot", plural(len(free), "posting time"))))
	}
	return nil
}

func liveProjects(ps []api.Project) []api.Project {
	var out []api.Project
	for _, p := range ps {
		if !p.Archived {
			out = append(out, p)
		}
	}
	return out
}

func dot(status string) string {
	switch status {
	case "COMPLETED":
		return green.Render("●")
	case "FAILED", "PARTIALLY_FAILED":
		return red.Render("●")
	case "PENDING_APPROVAL":
		return yellow.Render("●")
	}
	return brand.Render("●")
}

// ------------------------------------------------------------------ mktauto stats

var metricOrder = []string{"impressions", "reach", "views", "likes", "comments", "shares", "saves", "clicks"}

func metricsLine(m map[string]int64) string {
	var parts []string
	seen := map[string]bool{}
	for _, k := range metricOrder {
		if v, ok := m[k]; ok {
			parts = append(parts, fmt.Sprintf("%s %s", humanize.Comma(v), k))
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		parts = append(parts, fmt.Sprintf("%s %s", humanize.Comma(m[k]), strings.ReplaceAll(k, "_", " ")))
	}
	if len(parts) == 0 {
		return faint.Render("no numbers yet")
	}
	return strings.Join(parts, " · ")
}

func (e *Env) stats(ctx context.Context, args []string) error {
	fs := e.flags("stats", "stats [POST] [--days N] [--project NAME] [--refresh] [--links] [--json]")
	days := fs.Int("days", 30, "the last N days")
	project := fs.String("project", "", "only this project")
	refresh := fs.Bool("refresh", false, "with a post: ask the platforms for the latest numbers now")
	links := fs.Bool("links", false, "clicks on tracked links")
	asJSON := fs.Bool("json", false, "print JSON")
	var postArg string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		postArg, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	if _, err := e.me(ctx, c); err != nil {
		return err
	}
	if postArg != "" {
		id, err := e.resolvePost(ctx, c, postArg)
		if err != nil {
			return err
		}
		m, err := c.PostMetrics(ctx, id, *refresh)
		if err != nil {
			return serverRefused(err)
		}
		if *asJSON {
			return e.printJSON(m)
		}
		e.printf("%s %s\n", bold.Render("Total:"), metricsLine(m.Totals))
		for _, d := range m.Destinations {
			line := metricsLine(d.Metrics)
			switch {
			case !d.Supported:
				line = faint.Render(firstNonEmpty(d.Note, "the platform doesn't share numbers for this"))
			case d.Error != "":
				line = red.Render(d.Error)
			}
			when := ""
			if d.FetchedAt != nil {
				when = faint.Render(" · " + humanize.Time(*d.FetchedAt))
			}
			e.printf("  %-10s %-22s %s%s\n", platformShort(d.Platform), truncate(d.AccountName, 22), line, when)
		}
		return nil
	}
	if *links {
		list, err := c.Links(ctx)
		if err != nil {
			return serverRefused(err)
		}
		if *asJSON {
			return e.printJSON(list)
		}
		slices.SortFunc(list, func(a, b api.Link) int { return int(b.Clicks - a.Clicks) })
		for _, l := range list {
			var src []string
			for k, v := range l.BySource {
				src = append(src, fmt.Sprintf("%s %d", k, v))
			}
			sort.Strings(src)
			e.printf("%6s  %-28s %s  %s\n", humanize.Comma(l.Clicks), truncate(l.ShortURL, 28), faint.Render(truncate(l.TargetURL, 40)), faint.Render(strings.Join(src, " · ")))
		}
		if len(list) == 0 {
			e.printf("No tracked links yet.\n")
		}
		return nil
	}
	pid := ""
	if *project != "" {
		projects, err := c.Projects(ctx)
		if err != nil {
			return err
		}
		p, err := findProject(projects, *project)
		if err != nil {
			return err
		}
		pid = p.ID
	}
	a, err := c.Analytics(ctx, *days, pid)
	if api.IsProblem(err, 403, "cli_project") {
		return withCode(exitNotAllowed, errors.New("this terminal may only see some projects: add --project NAME"))
	}
	if err != nil {
		return serverRefused(err)
	}
	if *asJSON {
		return e.printJSON(a)
	}
	e.printf("%s %s\n", bold.Render(fmt.Sprintf("Last %d days · %s:", a.Days, plural(a.Posts, "post"))), metricsLine(a.Totals))
	var plats []string
	for p := range a.ByPlatform {
		plats = append(plats, p)
	}
	sort.Strings(plats)
	for _, p := range plats {
		e.printf("  %-16s %s\n", compose.PlatformLabel[p], metricsLine(a.ByPlatform[p]))
	}
	if len(a.Top) > 0 {
		e.printf("%s\n", bold.Render("Top posts"))
		for _, t := range a.Top {
			e.printf("  %-40s %s  %s\n", truncate(t.Title, 40), metricsLine(t.Totals), faint.Render(t.PostID[:8]))
		}
	}
	return nil
}

// ------------------------------------------------------------------ mktauto inbox

func (e *Env) inbox(ctx context.Context, args []string) error {
	fs := e.flags("inbox", "inbox [--all] [--read-all] [--json]   (failures, posts to finish yourself, approvals, sign-ins)")
	all := fs.Bool("all", false, "also the ones already read")
	readAll := fs.Bool("read-all", false, "mark everything as read")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	if _, err := e.me(ctx, c); err != nil {
		return err
	}
	if *readAll {
		if err := c.ReadAll(ctx); err != nil {
			return inboxRefused(err)
		}
		e.printf("%s Everything is marked as read\n", green.Render("✓"))
		return nil
	}
	items, err := c.Notifications(ctx, !*all, 50)
	if err != nil {
		return inboxRefused(err)
	}
	if *asJSON {
		return e.printJSON(items)
	}
	if len(items) == 0 {
		e.printf("%s Nothing new.\n", green.Render("✓"))
		return nil
	}
	if !e.Interactive {
		for _, n := range items {
			e.printf("%s %s  %s  %s\n", severityDot(n.Severity), faint.Render(n.CreatedAt.Local().Format("2 Jan 15:04")), n.Title, faint.Render(truncate(n.Body, 60)))
		}
		return nil
	}
	for {
		opts := make([]huh.Option[int], len(items))
		for i, n := range items {
			opts[i] = huh.NewOption(fmt.Sprintf("%s %s  %s  %s", severityDot(n.Severity), n.CreatedAt.Local().Format("2 Jan 15:04"), truncate(n.Title, 50),
				faint.Render(truncate(n.Body, 50))), i)
		}
		pick := 0
		if err := huh.NewSelect[int]().Title(fmt.Sprintf("Inbox · %s", plural(len(items), "item"))).Description("enter: what to do · esc: done").
			Options(opts...).Value(&pick).Height(min(len(opts)+3, 18)).Run(); err != nil {
			return nil
		}
		n := items[pick]
		e.printf("%s\n%s\n", bold.Render(n.Title), n.Body)
		acts := []huh.Option[string]{huh.NewOption("Mark as read", "read")}
		if n.PostID != "" {
			acts = append(acts, huh.NewOption("Show the post", "show"), huh.NewOption("Publish its failed accounts again", "retry"),
				huh.NewOption("Open it on the website", "open"))
		}
		acts = append(acts, huh.NewOption("Back", "back"))
		what := "read"
		if err := huh.NewSelect[string]().Title("What now?").Options(acts...).Value(&what).Run(); err != nil {
			continue
		}
		switch what {
		case "show":
			err = e.status(ctx, []string{n.PostID})
		case "retry":
			err = e.retry(ctx, []string{n.PostID})
		case "open":
			err = e.open(ctx, []string{n.PostID})
		}
		if err != nil {
			e.printf("%s %v\n", red.Render("✗"), err)
		}
		if what != "back" && n.ReadAt == nil {
			if err := c.MarkRead(ctx, n.ID); err == nil && !*all {
				items = slices.Delete(items, pick, pick+1)
			}
		}
		if len(items) == 0 {
			e.printf("%s Nothing new.\n", green.Render("✓"))
			return nil
		}
	}
}

func inboxRefused(err error) error {
	if api.IsProblem(err, 403, "cli_project") {
		return errors.New("this terminal is limited to some projects: the inbox is on the website (or in the app)")
	}
	return serverRefused(err)
}

func severityDot(s string) string {
	switch s {
	case "ERROR", "CRITICAL":
		return red.Render("●")
	case "WARNING":
		return yellow.Render("●")
	}
	return brand.Render("●")
}

// unreadBadge for the menu: " (3 new)" or "".
func (e *Env) unreadBadge(ctx context.Context) string {
	c, err := e.client()
	if err != nil {
		return ""
	}
	n, err := c.UnreadCount(ctx)
	if err != nil || n == 0 {
		return ""
	}
	return yellow.Render(fmt.Sprintf(" (%d new)", n))
}

// ------------------------------------------------------------------ mktauto duplicate, stop-repeat

func (e *Env) duplicate(ctx context.Context, args []string) error {
	fs := e.flags("duplicate", "duplicate POST [--at \"YYYY-MM-DD HH:MM\" | --next-slot]   (a copy as a new draft; a time schedules it)")
	at := fs.String("at", "", "schedule the copy at this time")
	next := fs.Bool("next-slot", false, "schedule the copy in the next free posting time")
	c, p, err := e.postArg(ctx, fs, args)
	if err != nil {
		return err
	}
	cp, err := c.Duplicate(ctx, p.ID)
	if err != nil {
		return serverRefused(err)
	}
	e.printf("%s Copied as a new draft: %s %s\n", green.Render("✓"), bold.Render(cp.Title()), faint.Render(cp.ID[:8]))
	switch {
	case *next:
		return e.schedule(ctx, []string{cp.ID, "--next-slot"})
	case *at != "":
		return e.schedule(ctx, []string{cp.ID, "--at", *at})
	}
	return nil
}

func (e *Env) stopRepeat(ctx context.Context, args []string) error {
	fs := e.flags("stop-repeat", "stop-repeat POST   (no more repeats; posts already planned stay)")
	c, p, err := e.postArg(ctx, fs, args)
	if err != nil {
		return err
	}
	if p.RepeatEvery == 0 {
		return errors.New("this post doesn't repeat")
	}
	if _, err := c.StopRepeat(ctx, p.ID); err != nil {
		return serverRefused(err)
	}
	e.printf("%s %s won't repeat any more\n", green.Render("✓"), bold.Render(p.Title()))
	return nil
}
