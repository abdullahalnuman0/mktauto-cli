package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/abdullahalnuman0/mktauto-cli/internal/history"
	"github.com/charmbracelet/huh"
)

// Listing what can be chosen (projects, accounts, boards) and managing saved posts (ADR-076).

func (e *Env) flags(name, usageLine string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.Err)
	fs.Usage = func() {
		fmt.Fprintf(e.Err, "Usage: mktauto %s\n", usageLine)
		fs.PrintDefaults()
	}
	return fs
}

func (e *Env) printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ------------------------------------------------------------------ projects, accounts, boards

func (e *Env) projects(ctx context.Context, args []string) error {
	fs := e.flags("projects", "projects [--json]")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	list, err := c.Projects(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return e.printJSON(list)
	}
	for _, p := range list {
		extra := p.Timezone
		if p.Archived {
			extra += " · archived"
		}
		e.printf("%-28s %s  %s\n", bold.Render(p.Name), faint.Render(extra), faint.Render(p.ID))
	}
	if len(list) == 0 {
		e.printf("No projects available to this terminal.\n")
	}
	return nil
}

func findProject(list []api.Project, term string) (*api.Project, error) {
	for i, p := range list {
		if strings.EqualFold(p.ID, term) || strings.EqualFold(p.Name, term) {
			return &list[i], nil
		}
	}
	names := make([]string, len(list))
	for i, p := range list {
		names[i] = p.Name
	}
	return nil, fmt.Errorf("no project %q (one of: %s)", term, strings.Join(names, ", "))
}

func (e *Env) accounts(ctx context.Context, args []string) error {
	fs := e.flags("accounts", "accounts [--project NAME] [--json]")
	project := fs.String("project", "", "only this project's accounts (with its default ticks and order)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	if *project != "" {
		list, err := c.Projects(ctx)
		if err != nil {
			return err
		}
		p, err := findProject(list, *project)
		if err != nil {
			return err
		}
		accs, err := c.ProjectAccounts(ctx, p.ID)
		if err != nil {
			return err
		}
		slices.SortFunc(accs, func(a, b api.ProjectAccount) int { return a.SortOrder - b.SortOrder })
		if *asJSON {
			return e.printJSON(accs)
		}
		for _, a := range accs {
			e.printAccount(a.Platform, a.DisplayName, a.Handle, a.Status, a.PublishMode, a.AccountID, a.DefaultSelected)
		}
		e.printf("%s\n", faint.Render("✓ = ticked by default · use names, @handles, ids or platforms with --accounts"))
		return nil
	}
	accs, err := c.Accounts(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return e.printJSON(accs)
	}
	for _, a := range accs {
		e.printAccount(a.Platform, a.DisplayName, a.Handle, a.Status, a.PublishMode, a.ID, false)
	}
	return nil
}

func (e *Env) printAccount(platform, name, handle, status, mode, id string, def bool) {
	tick := " "
	if def {
		tick = green.Render("✓")
	}
	who := name
	if handle != "" && !strings.EqualFold(strings.TrimPrefix(name, "@"), handle) {
		who += " @" + handle
	}
	state := strings.ToLower(strings.ReplaceAll(status, "_", " "))
	if status != "ACTIVE" && status != "" {
		state = yellow.Render(state)
	}
	if mode == "MANUAL" {
		state += " · you post it"
	}
	e.printf("%s %-16s %-32s %-22s %s\n", tick, compose.PlatformLabel[platform], truncate(who, 32), state, faint.Render(id))
}

func (e *Env) boards(ctx context.Context, args []string) error {
	fs := e.flags("boards", "boards ACCOUNT [--json]   (a Pinterest account: name, @handle or id)")
	asJSON := fs.Bool("json", false, "print JSON")
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	name = firstNonEmpty(name, fs.Arg(0))
	c, err := e.client()
	if err != nil {
		return err
	}
	accs, err := c.Accounts(ctx)
	if err != nil {
		return err
	}
	var pins []api.Account
	for _, a := range accs {
		if a.Platform == "PINTEREST" {
			pins = append(pins, a)
		}
	}
	term := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "@")
	var chosen []api.Account
	for _, a := range pins {
		if term == "" || strings.EqualFold(a.ID, term) || strings.ToLower(a.DisplayName) == term || strings.ToLower(a.Handle) == term {
			chosen = append(chosen, a)
		}
	}
	if len(chosen) == 0 {
		return fmt.Errorf("no Pinterest account %q", name)
	}
	type out struct {
		Account string      `json:"account"`
		Default string      `json:"defaultBoard"`
		Boards  []api.Board `json:"boards"`
	}
	var res []out
	for _, a := range chosen {
		b, def := a.Boards()
		res = append(res, out{a.DisplayName, def, b})
	}
	if *asJSON {
		return e.printJSON(res)
	}
	for _, r := range res {
		e.printf("%s\n", bold.Render(r.Account))
		for _, b := range r.Boards {
			mark := ""
			if b.ID == r.Default {
				mark = green.Render(" (default)")
			}
			e.printf("  %-32s %s%s\n", b.Name, faint.Render(b.ID), mark)
		}
		if len(r.Boards) == 0 {
			e.printf("  %s\n", faint.Render("no boards loaded: open the account on the website to refresh them"))
		}
	}
	return nil
}

// ------------------------------------------------------------------ finding a post

var hexPrefix = regexp.MustCompile(`^[0-9a-fA-F-]{4,36}$`)

// resolvePost accepts a full id, the start of one (as `mktauto status` prints), or "last" (the newest post made
// from this folder, else the newest anywhere).
func (e *Env) resolvePost(ctx context.Context, c *api.Client, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "":
		return "", errors.New("which post? Give its id (mktauto status lists them) or \"last\"")
	case strings.EqualFold(arg, "last"):
		if h, _ := history.For(e.Dir); len(h) > 0 {
			return h[0].PostID, nil
		}
		posts, err := recentPosts(ctx, c, 90)
		if err != nil {
			return "", err
		}
		if len(posts) == 0 {
			return "", errors.New("no posts yet")
		}
		return posts[0].ID, nil
	case len(arg) == 36:
		return arg, nil
	case !hexPrefix.MatchString(arg):
		return "", fmt.Errorf("%q isn't a post id", arg)
	}
	posts, err := recentPosts(ctx, c, 180)
	if err != nil {
		return "", err
	}
	var found []api.Post
	for _, p := range posts {
		if strings.HasPrefix(strings.ToLower(p.ID), strings.ToLower(arg)) {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no post starting with %s in the last 180 days", arg)
	case 1:
		return found[0].ID, nil
	}
	return "", fmt.Errorf("%s matches %d posts: give more of the id", arg, len(found))
}

// postArg parses the command's flags and finds the post named by its first argument (flags may come after it).
func (e *Env) postArg(ctx context.Context, fs *flag.FlagSet, args []string) (*api.Client, *api.Post, error) {
	var id string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	if id == "" {
		id = fs.Arg(0)
	}
	c, err := e.client()
	if err != nil {
		return nil, nil, err
	}
	if _, err := e.me(ctx, c); err != nil {
		return nil, nil, err
	}
	full, err := e.resolvePost(ctx, c, id)
	if err != nil {
		return nil, nil, err
	}
	p, err := c.Post(ctx, full)
	if err != nil {
		return nil, nil, err
	}
	return c, p, nil
}

// confirm asks in a terminal unless --yes; elsewhere a risky action needs --yes.
func (e *Env) confirm(question string, yes, risky bool) error {
	if yes {
		return nil
	}
	if !e.Interactive {
		if risky {
			return errors.New("add --yes to do this without a terminal to confirm in")
		}
		return nil
	}
	ok := false
	if err := huh.NewConfirm().Title(question).Affirmative("Yes").Negative("No").Value(&ok).Run(); err != nil {
		return err
	}
	if !ok {
		return errSilent
	}
	return nil
}

// serverRefused explains an action this server doesn't allow terminals yet (an older backend).
func serverRefused(err error) error {
	if api.IsProblem(err, 403, "cli_not_allowed") {
		return errors.New("this MKTAuto server doesn't allow that from a terminal yet: update the server, or do it on the website")
	}
	return err
}

// ------------------------------------------------------------------ schedule, unschedule, publish, cancel, delete, edit

func (e *Env) schedule(ctx context.Context, args []string) error {
	fs := e.flags("schedule", "schedule POST (--at \"YYYY-MM-DD HH:MM\" | --next-slot)   (also moves a scheduled post)")
	at := fs.String("at", "", "date and time in the project's timezone")
	next := fs.Bool("next-slot", false, "the project's next free posting slot")
	c, p, err := e.postArg(ctx, fs, args)
	if err != nil {
		return err
	}
	tz := p.Timezone
	var local string
	switch {
	case *next && *at != "":
		return errors.New("use --at or --next-slot, not both")
	case *next:
		slot, err := c.NextSlot(ctx, p.ProjectID)
		if err != nil {
			return err
		}
		local = slot.LocalDateTime
	case *at != "":
		if local, err = compose.LocalDateTime(*at); err != nil {
			return err
		}
		if !compose.InFuture(local, tz, time.Now()) {
			return fmt.Errorf("%s has passed in %s", *at, tz)
		}
	default:
		return errors.New("when? add --at \"YYYY-MM-DD HH:MM\" or --next-slot")
	}
	in := &api.Schedule{LocalDateTime: local, Timezone: tz}
	res, err := c.Schedule(ctx, p.ID, in)
	if err != nil && p.Status == "SCHEDULED" && api.IsProblem(err, 409, "") {
		// Moving a scheduled post: back to a draft, then scheduled at the new time.
		if _, uerr := c.Unschedule(ctx, p.ID); uerr != nil {
			return uerr
		}
		if res, err = c.Schedule(ctx, p.ID, in); err != nil {
			return fmt.Errorf("%v (the post is a draft now)", err)
		}
	}
	if err != nil {
		e.explainValidation(err)
		return err
	}
	when := strings.Replace(local[:16], "T", " ", 1)
	if res.Status == "PENDING_APPROVAL" {
		e.printf("%s Sent for approval: it goes out at %s (%s) once an admin approves it.\n", green.Render("✓"), when, tz)
		return nil
	}
	e.printf("%s %s scheduled for %s (%s)\n", green.Render("✓"), bold.Render(res.Title()), when, tz)
	return nil
}

func (e *Env) explainValidation(err error) {
	var prob *api.Problem
	if errors.As(err, &prob) && prob.Validation != nil {
		for _, d := range prob.Validation.Destinations {
			for _, i := range d.Issues {
				if i.Severity == "ERROR" {
					e.printf("  %s %s\n", red.Render("✗"), i.Message)
				}
			}
		}
	}
}

func (e *Env) unschedule(ctx context.Context, args []string) error {
	fs := e.flags("unschedule", "unschedule POST   (back to a draft; also withdraws it from approval)")
	c, p, err := e.postArg(ctx, fs, args)
	if err != nil {
		return err
	}
	res, err := c.Unschedule(ctx, p.ID)
	if err != nil {
		return err
	}
	e.printf("%s %s is a draft again\n", green.Render("✓"), bold.Render(res.Title()))
	return nil
}

func (e *Env) publish(ctx context.Context, args []string) error {
	fs := e.flags("publish", "publish POST [--yes] [--wait]   (publish a saved post now)")
	yes := fs.Bool("yes", false, "don't ask")
	wait := fs.Bool("wait", e.Interactive, "wait for each account's result (in a terminal by default; --wait=false not)")
	c, p, err := e.postArg(ctx, fs, args)
	if err != nil {
		return err
	}
	if err := e.confirm("Publish \""+truncate(p.Title(), 50)+"\" now?", *yes, false); err != nil {
		return err
	}
	res, err := c.PublishNow(ctx, p.ID)
	if err != nil {
		e.explainValidation(err)
		return err
	}
	if res.Status == "PENDING_APPROVAL" {
		e.printf("%s Sent for approval: it goes out once an admin approves it.\n", green.Render("✓"))
		return nil
	}
	e.printf("%s Publishing…\n", green.Render("✓"))
	if *wait {
		(&poster{env: e, c: c}).watch(ctx, p.ID)
	}
	return nil
}

func (e *Env) cancel(ctx context.Context, args []string) error {
	fs := e.flags("cancel", "cancel POST [--yes]   (stop what isn't published yet; published accounts stay)")
	yes := fs.Bool("yes", false, "don't ask")
	c, p, err := e.postArg(ctx, fs, args)
	if err != nil {
		return err
	}
	if err := e.confirm("Cancel \""+truncate(p.Title(), 50)+"\"? Accounts not published yet won't get it.", *yes, true); err != nil {
		return err
	}
	res, err := c.Cancel(ctx, p.ID)
	if err != nil {
		return serverRefused(err)
	}
	e.printf("%s Cancelled\n", green.Render("✓"))
	e.printPost(res)
	return nil
}

func (e *Env) deletePost(ctx context.Context, args []string) error {
	fs := e.flags("delete", "delete POST [--yes]   (drafts only; scheduled posts: cancel or unschedule)")
	yes := fs.Bool("yes", false, "don't ask")
	c, p, err := e.postArg(ctx, fs, args)
	if err != nil {
		return err
	}
	if p.Status != "DRAFT" {
		return fmt.Errorf("only drafts can be deleted (this one is %s): use mktauto unschedule or mktauto cancel", strings.ToLower(strings.ReplaceAll(p.Status, "_", " ")))
	}
	if err := e.confirm("Delete the draft \""+truncate(p.Title(), 50)+"\"?", *yes, true); err != nil {
		return err
	}
	if err := c.DeletePost(ctx, p.ID); err != nil {
		return serverRefused(err)
	}
	e.printf("%s Draft deleted\n", green.Render("✓"))
	return nil
}

func (e *Env) edit(ctx context.Context, args []string) error {
	fs := e.flags("edit", "edit POST [--caption TEXT | --caption-file FILE] [--hashtags TAGS] [--link URL] [--name NAME]\n"+
		"  (drafts, and scheduled posts that haven't started publishing; per-account texts: on the website)")
	caption := fs.String("caption", "", "new main caption")
	captionFile := fs.String("caption-file", "", "read the new caption from a file")
	hashtags := fs.String("hashtags", "", "new hashtags (replace the old ones; \"-\" removes them)")
	link := fs.String("link", "", "new link")
	name := fs.String("name", "", "new internal name")
	c, p, err := e.postArg(ctx, fs, args)
	if err != nil {
		return err
	}
	fields := map[string]any{}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["caption-file"] {
		data, err := os.ReadFile(*captionFile)
		if err != nil {
			return err
		}
		fields["baseCaption"] = strings.TrimSpace(string(data))
	} else if set["caption"] {
		fields["baseCaption"] = strings.TrimSpace(*caption)
	}
	if set["hashtags"] {
		tags := []string{}
		if strings.TrimSpace(*hashtags) != "-" {
			tags = compose.ParseHashtags(*hashtags)
		}
		fields["baseHashtags"] = tags
	}
	if set["link"] {
		fields["linkUrl"] = strings.TrimSpace(*link)
	}
	if set["name"] {
		fields["internalTitle"] = strings.TrimSpace(*name)
	}
	if len(fields) == 0 {
		return errors.New("nothing to change: add --caption, --caption-file, --hashtags, --link or --name")
	}
	res, err := c.UpdatePost(ctx, p.ID, fields)
	if err != nil {
		e.explainValidation(err)
		return err
	}
	e.printf("%s Updated %s\n", green.Render("✓"), bold.Render(res.Title()))
	return nil
}

// ------------------------------------------------------------------ actions on one post (dashboard and menu)

type postAction struct{ key, label, needs string }

// actionsFor: what can be done with a post in its status, by this terminal.
func actionsFor(p *api.Post, me *api.Me) []postAction {
	var acts []postAction
	switch p.Status {
	case "DRAFT":
		acts = []postAction{{"s", "schedule", "SCHEDULE"}, {"p", "publish now", "PUBLISH"}, {"e", "edit caption", "DRAFT"}, {"d", "delete", "DRAFT"}}
	case "SCHEDULED", "PENDING_APPROVAL":
		acts = []postAction{{"s", "move", "SCHEDULE"}, {"u", "unschedule", "SCHEDULE"}, {"p", "publish now", "PUBLISH"}, {"e", "edit caption", "DRAFT"},
			{"c", "cancel", "SCHEDULE"}}
	case "PUBLISHING", "PARTIALLY_FAILED", "FAILED":
		acts = []postAction{{"r", "retry failed", "PUBLISH"}, {"c", "cancel the rest", "SCHEDULE"}}
	}
	var out []postAction
	for _, a := range acts {
		if me == nil || me.Allows(a.needs) {
			out = append(out, a)
		}
	}
	out = append(out, postAction{"D", "duplicate", "DRAFT"})
	if p.RepeatEvery > 0 && (me == nil || me.Allows("SCHEDULE")) {
		out = append(out, postAction{"x", "stop repeating", "SCHEDULE"})
	}
	return append(out, postAction{"o", "open on the website", ""})
}

// runPostAction does one action, asking what it needs (a time, the new caption, a confirmation).
func (e *Env) runPostAction(ctx context.Context, p *api.Post, key string) error {
	id := p.ID
	switch key {
	case "s":
		how, at := "slot", time.Now().Add(time.Hour).Format("2006-01-02 15:00")
		if p.LocalDateTime != "" {
			at = strings.Replace(p.LocalDateTime[:min(16, len(p.LocalDateTime))], "T", " ", 1)
		}
		if err := huh.NewSelect[string]().Title("When: "+truncate(p.Title(), 50)).Options(huh.NewOption("Next free posting slot", "slot"),
			huh.NewOption("At a time I choose", "at")).Value(&how).Run(); err != nil {
			return nil
		}
		if how == "slot" {
			return e.schedule(ctx, []string{id, "--next-slot"})
		}
		if err := huh.NewInput().Title("Date and time (" + p.Timezone + ")").Description("YYYY-MM-DD HH:MM").Value(&at).
			Validate(func(s string) error { _, err := compose.LocalDateTime(s); return err }).Run(); err != nil {
			return nil
		}
		return e.schedule(ctx, []string{id, "--at", at})
	case "e":
		text := p.BaseCaption
		if err := huh.NewText().Title("Caption: " + truncate(p.Title(), 50)).Description("ctrl+e opens your editor").Value(&text).Lines(8).CharLimit(63000).
			ExternalEditor(true).Run(); err != nil || text == p.BaseCaption {
			return nil
		}
		return e.edit(ctx, []string{id, "--caption", text})
	case "p":
		return e.publish(ctx, []string{id})
	case "u":
		return e.unschedule(ctx, []string{id})
	case "c":
		return e.cancel(ctx, []string{id})
	case "d":
		return e.deletePost(ctx, []string{id})
	case "r":
		return e.retry(ctx, []string{id})
	case "o":
		return e.open(ctx, []string{id})
	case "D":
		return e.duplicate(ctx, []string{id})
	case "x":
		return e.stopRepeat(ctx, []string{id})
	}
	return nil
}

// managePosts: the menu's "Saved posts" is the posts dashboard.
func (e *Env) managePosts(ctx context.Context) error { return e.posts(ctx, nil) }
