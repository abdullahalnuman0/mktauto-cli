package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/auth"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/abdullahalnuman0/mktauto-cli/internal/history"
	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
	"github.com/abdullahalnuman0/mktauto-cli/internal/ui"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
)

func (e *Env) whoami(ctx context.Context) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	m, err := e.me(ctx, c)
	if err != nil {
		return err
	}
	e.printf("%s (%s) on %s\n", bold.Render(m.DisplayName), m.Email, e.Server)
	e.printf("Terminal: %s · %s\n", m.Session.Name, permissionText(m.Session.Permission))
	if len(m.Session.ProjectIDs) > 0 {
		e.printf("Limited to %s\n", plural(len(m.Session.ProjectIDs), "project"))
	}
	if m.Session.DailyPostLimit != nil {
		e.printf("Posts in the last 24 h: %d of %d\n", m.PostsLast24h, *m.Session.DailyPostLimit)
	}
	if m.RequiresApproval {
		e.printf("%s Your posts are checked by an admin before they go out.\n", yellow.Render("!"))
	}
	e.printf("%s\n", faint.Render(fmt.Sprintf("Signed in until %s · %s and %s from this terminal",
		m.Session.ExpiresAt.Local().Format("2 Jan 2006"), plural(m.Session.PostsCreated, "post"), plural(m.Session.FilesUploaded, "file"))))
	return nil
}

func (e *Env) menu(ctx context.Context) error {
	if !e.Interactive {
		fmt.Fprint(e.Out, usage)
		return nil
	}
	tagline := "  Post from a folder to all your social accounts · " + faint.Render("v"+strings.TrimPrefix(e.Version, "v"))
	_, tokErr := e.token()
	if tokErr != nil {
		tagline += faint.Render(" · not signed in yet")
	}
	ui.Banner(tagline, e.width(), e.animate())
	e.printf("\n")
	if tokErr != nil {
		if err := e.login(ctx, nil); err != nil {
			return err
		}
		e.printf("\n")
	}
	files, _ := media.Scan(e.Dir)
	for {
		choice := "post"
		images, videos := countKinds(files)
		postLabel := fmt.Sprintf("Post from this folder (%s, %s)", plural(images, "image"), plural(videos, "video"))
		if len(files) == 0 {
			postLabel = "Post (no images or videos in this folder: text only)"
		}
		err := huh.NewSelect[string]().Title("Folder "+shortPath(e.Dir)).Options(
			huh.NewOption(postLabel, "post"),
			huh.NewOption("Status of recent posts", "status"),
			huh.NewOption("What was posted from this folder", "history"),
			huh.NewOption("Who am I", "whoami"),
			huh.NewOption("Sign out", "logout"),
			huh.NewOption("Quit", "quit"),
		).Value(&choice).Run()
		if err != nil || choice == "quit" {
			return nil
		}
		switch choice {
		case "post":
			err = e.post(ctx, nil)
		case "status":
			err = e.status(ctx, nil)
		case "history":
			err = e.history(nil)
		case "whoami":
			err = e.whoami(ctx)
		case "logout":
			return e.logout(ctx)
		}
		switch {
		case errors.Is(err, errBack): // Esc on the first question: back to this menu
		case errors.Is(err, huh.ErrUserAborted):
			e.printf("%s\n", faint.Render("Cancelled: nothing was saved."))
		case err != nil && !errors.Is(err, errSilent):
			fmt.Fprintln(e.Err, red.Render("✗ ")+err.Error())
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		e.printf("\n")
	}
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// ---- status

var doneStatuses = map[string]bool{"PUBLISHED": true, "FAILED": true, "MANUAL_DONE": true, "CANCELLED": true, "SKIPPED": true,
	"NEEDS_VERIFICATION": true, "MANUAL_REQUIRED": true, "DRAFT": true, "SCHEDULED": true}

func statusText(s string) string {
	switch s {
	case "PUBLISHED", "MANUAL_DONE", "COMPLETED":
		return green.Render(strings.ToLower(strings.ReplaceAll(s, "_", " ")))
	case "FAILED", "PARTIALLY_FAILED", "NEEDS_VERIFICATION":
		return red.Render(strings.ToLower(strings.ReplaceAll(s, "_", " ")))
	case "MANUAL_REQUIRED", "PENDING_APPROVAL", "RETRY_WAIT":
		return yellow.Render(strings.ToLower(strings.ReplaceAll(s, "_", " ")))
	}
	return strings.ToLower(strings.ReplaceAll(s, "_", " "))
}

func (e *Env) status(ctx context.Context, args []string) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	if len(args) > 0 {
		p, err := c.Post(ctx, args[0])
		if err != nil {
			return err
		}
		e.printPost(p)
		return nil
	}
	if _, err := e.me(ctx, c); err != nil {
		return err
	}
	posts, err := c.Posts(ctx, time.Now().Add(-14*24*time.Hour))
	if err != nil {
		return err
	}
	if len(posts) == 0 {
		e.printf("No posts in the last 14 days.\n")
		return nil
	}
	if len(posts) > 15 {
		posts = posts[:15]
	}
	for _, p := range posts {
		when := "not scheduled"
		if p.ScheduledAt != nil {
			when = p.ScheduledAt.Local().Format("Mon 2 Jan 15:04")
		}
		counts := map[string]int{}
		for _, d := range p.Destinations {
			counts[d.Status]++
		}
		var parts []string
		for _, s := range []string{"PUBLISHED", "SCHEDULED", "QUEUED", "PUBLISHING", "PROCESSING", "RETRY_WAIT", "MANUAL_REQUIRED", "FAILED", "NEEDS_VERIFICATION", "DRAFT"} {
			if counts[s] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[s], statusText(s)))
			}
		}
		e.printf("%s  %-40s %s  %s\n", faint.Render(p.ID[:8]), truncate(p.Title(), 40), faint.Render(when), strings.Join(parts, ", "))
	}
	e.printf("%s\n", faint.Render("Details: mktauto status <id>  ·  website: mktauto open <id>"))
	return nil
}

func (e *Env) printPost(p *api.Post) {
	e.printf("%s  %s\n", bold.Render(p.Title()), statusText(p.Status))
	for _, d := range p.Destinations {
		line := fmt.Sprintf("  %-14s %-24s %-10s %s", compose.PlatformLabel[d.Platform], truncate(d.AccountName, 24), compose.FormatLabel[d.Format], statusText(d.Status))
		if d.PublishedURL != "" {
			line += "  " + faint.Render(d.PublishedURL)
		} else if d.LastErrorMessage != "" && (d.Status == "FAILED" || d.Status == "RETRY_WAIT" || d.Status == "NEEDS_VERIFICATION") {
			line += "  " + red.Render(truncate(d.LastErrorMessage, 70))
		} else if d.Status == "MANUAL_REQUIRED" {
			line += "  " + yellow.Render("post it yourself, then mark it done on the website")
		}
		e.printf("%s\n", line)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ---- retry

func (e *Env) retry(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: mktauto retry <post id>")
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	p, err := c.Post(ctx, args[0])
	if err != nil {
		return err
	}
	n := 0
	for _, d := range p.Destinations {
		if d.Status != "FAILED" {
			continue
		}
		if err := c.RetryDestination(ctx, d.ID); err != nil {
			return fmt.Errorf("%s: %w", d.AccountName, err)
		}
		n++
		e.printf("%s %s (%s) queued again\n", green.Render("↻"), d.AccountName, compose.PlatformLabel[d.Platform])
	}
	if n == 0 {
		e.printf("Nothing failed in this post.\n")
	}
	return nil
}

// ---- history

func (e *Env) history(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	all := fs.Bool("all", false, "every folder, not only this one")
	if err := fs.Parse(args); err != nil {
		return err
	}
	folder := e.Dir
	if *all {
		folder = ""
	}
	entries, err := history.For(folder)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		e.printf("Nothing was posted from this folder with mktauto yet.\n")
		return nil
	}
	for _, h := range entries {
		names := make([]string, len(h.Files))
		for i, f := range h.Files {
			names[i] = f.Name
		}
		e.printf("%s  %-10s %s · %s\n", faint.Render(h.At.Local().Format("2 Jan 15:04")), h.Action, bold.Render(h.Project), strings.Join(h.Accounts, ", "))
		if *all {
			e.printf("   %s\n", faint.Render(shortPath(h.Folder)))
		}
		if len(names) > 0 {
			e.printf("   %s\n", faint.Render(strings.Join(names, ", ")))
		}
		e.printf("   %s\n", faint.Render("post "+h.PostID))
	}
	return nil
}

// ---- open

func (e *Env) open(ctx context.Context, args []string) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	m, err := e.me(ctx, c)
	if err != nil {
		return err
	}
	url := m.WebURL + "/dashboard"
	if len(args) > 0 {
		url = m.WebURL + "/posts/" + args[0]
	}
	if !auth.OpenBrowser(url) {
		e.printf("Open: %s\n", url)
	}
	return nil
}

// ---- doctor

func (e *Env) doctor(ctx context.Context) error {
	ok := func(s string) { e.printf("%s %s\n", green.Render("✓"), s) }
	warn := func(s string) { e.printf("%s %s\n", yellow.Render("!"), s) }
	bad := func(s string) { e.printf("%s %s\n", red.Render("✗"), s) }
	ok("mktauto " + e.Version + " · " + auth.ThisDevice(e.Version).OS + "/" + auth.ThisDevice(e.Version).Arch)
	dir, _ := auth.Dir()
	ok("Settings: " + dir)
	ok("Server: " + e.Server)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.Server+"/api/cli/me", nil)
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		bad("Can't reach the server: " + err.Error())
	} else {
		res.Body.Close()
		ok("The server answers")
		if d, err := http.ParseTime(res.Header.Get("Date")); err == nil {
			if skew := time.Since(d); skew > 2*time.Minute || skew < -2*time.Minute {
				warn(fmt.Sprintf("This computer's clock is off by %s: scheduled times and sign-in may misbehave", skew.Round(time.Second)))
			} else {
				ok("Clock in sync")
			}
		}
	}
	ok("Session kept in " + e.Store.Where())
	if c, err := e.client(); err != nil {
		warn("Not signed in: run `mktauto login`")
	} else if m, err := e.me(ctx, c); err != nil {
		bad(err.Error())
	} else {
		ok(fmt.Sprintf("Signed in as %s (%s)", m.DisplayName, permissionText(m.Session.Permission)))
	}
	files, err := media.Scan(e.Dir)
	if err != nil {
		warn("Can't read this folder: " + err.Error())
	} else {
		ok(fmt.Sprintf("This folder: %s that can be posted", plural(len(files), "file")))
	}
	return nil
}

// width of the terminal (0 when unknown).
func (e *Env) width() int {
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return 0
	}
	return w
}

// animate: moving pictures only in a real terminal, and never when asked not to.
func (e *Env) animate() bool {
	return e.Interactive && os.Getenv("MKTAUTO_NO_ANIMATION") == "" && os.Getenv("TERM") != "dumb"
}
