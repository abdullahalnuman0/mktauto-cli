// Package app is the mktauto command line: sign-in, the post wizard and the smaller commands.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/auth"
	"github.com/abdullahalnuman0/mktauto-cli/internal/update"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// Env is what every command needs.
type Env struct {
	Version     string
	Out, Err    io.Writer
	Data        io.Writer // --json data: os.Stdout (also with --quiet), a buffer for mktauto mcp
	In          *os.File
	Interactive bool
	Config      *auth.Config
	Store       auth.TokenStore
	Server      string
	Dir         string // the folder mktauto runs in
	mcpLevel    int    // mktauto mcp --allow (0 read … 3 publish)
}

var (
	bold   = lipgloss.NewStyle().Bold(true)
	faint  = lipgloss.NewStyle().Faint(true)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	brand  = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
)

// errSilent: the message was already shown.
var errSilent = errors.New("")

const usage = `mktauto: post to your MKTAuto accounts from a folder.

Usage:
  mktauto                    menu (signs in first when needed)
  mktauto post [flags]       post files from this folder (no flags: step by step; -h lists every flag)
  mktauto batch              one post per file or subfolder, in the next free posting times (or drafts)
  mktauto import FILE.csv    one post per spreadsheet row (--example prints one)
  mktauto draft --from-git   a "what's new" post from an app's commits since its last release
  mktauto watch              keeps running: each new file dropped here becomes a post
  mktauto preset save NAME   save post flags (project, accounts, hashtags…) to use anywhere with --preset NAME

Find what to post to:
  mktauto projects           projects this terminal may post to
  mktauto accounts           accounts (--project NAME: that project's, with its default ticks)
  mktauto boards ACCOUNT     a Pinterest account's boards

Saved posts (POST = an id, its first characters as status shows them, or "last"):
  mktauto posts              full screen: search, filter by status, one key to schedule/publish/edit/cancel/delete
  mktauto calendar           what's planned, day by day, with the free posting times
  mktauto stats [POST]       results: reach, likes, comments… (--links: clicks on tracked links)
  mktauto balance            your credit: balance, what posts cost, the last days' charges (top up: mktauto billing)
  mktauto inbox              failures, posts to finish yourself, approvals
  mktauto best-times         when your posts did best, by day and time (a suggestion from your own results)
  mktauto status [POST]      recent posts, or one post's accounts
  mktauto edit POST          change caption, hashtags, link or name (--caption, --hashtags, --link, --name)
  mktauto schedule POST      schedule or move it (--at "YYYY-MM-DD HH:MM" or --next-slot)
  mktauto unschedule POST    back to a draft
  mktauto publish POST       publish now
  mktauto cancel POST        stop what isn't published yet
  mktauto delete POST        delete a draft
  mktauto duplicate POST     a copy as a new draft (--at or --next-slot schedules it)
  mktauto stop-repeat POST   no more repeats of a repeating post
  mktauto retry POST         publish failed accounts again
  mktauto open [POST]        open the website (or the post) in the browser
  mktauto history [--all]    what was posted from this folder

Write:
  mktauto ai ACTION TEXT     AI caption help: improve, shorten, professional, casual, hashtags, translate

Android apps (App Ops):
  mktauto release-summary    summarise a release build (permissions, SDKs, versions) for the app's Builds tab (--upload)

This computer:
  mktauto login              sign in (browser approval or QR code; --code for a login code)
  mktauto logout             sign out this computer
  mktauto whoami             who and what this terminal may do
  mktauto doctor             check the connection and this installation
  mktauto update             install the latest version (--version vX.Y.Z for another one)
  mktauto completion SHELL   tab completion for bash, zsh, fish or powershell
  mktauto mcp                connect AI assistants (Claude…): --allow read|drafts|schedule|publish
  mktauto version

Global (before the command): --server URL (or MKTAUTO_SERVER) another MKTAuto server · --quiet (-q) only errors and
--json output · --no-color (or NO_COLOR=1). Most commands take --json.
` + exitCodesHelp + `Run "mktauto <command> -h" for a command's flags. More: https://mktauto.site/download
`

// Run executes the command line and returns the exit code.
func Run(args []string, version string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	env, err := newEnv(version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mktauto:", err)
		return 1
	}
	// Global flags come before the command: --server URL, --quiet (-q), --no-color.
	server, quiet := "", false
loop:
	for len(args) > 0 {
		switch a := args[0]; {
		case a == "--quiet" || a == "-q":
			quiet, args = true, args[1:]
		case a == "--no-color":
			_ = os.Setenv("NO_COLOR", "1") // lipgloss reads it before the first coloured text
			args = args[1:]
		case strings.HasPrefix(a, "--server="):
			server, args = strings.TrimPrefix(a, "--server="), args[1:]
		case a == "--server" && len(args) > 1:
			server, args = args[1], args[2:]
		case a == "--server":
			args = args[1:]
		default:
			break loop
		}
	}
	if quiet {
		env.Out = io.Discard // messages; --json output and errors still show
	}
	env.Server = api.NormalizeServer(env.Config.ServerURL(server))
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	// Never sign in or send the session token over plain http:// (only to this computer, for development).
	if err := api.CheckServer(env.Server); err != nil && !offlineCommand(cmd) {
		fmt.Fprintln(env.Err, red.Render("✗ ")+err.Error()+".\nSet the server with --server https://… or MKTAUTO_SERVER.")
		return exitUsage
	}
	if exe, err := os.Executable(); err == nil {
		update.Cleanup(exe) // the program a Windows update left behind
	}
	var notice <-chan string
	if cmd != "update" && cmd != "version" && cmd != "--version" && cmd != "-v" && cmd != "help" && cmd != "-h" && cmd != "--help" &&
		cmd != "__complete" && cmd != "completion" && cmd != "mcp" {
		notice = env.noticeUpdate(ctx)
	}
	err = env.dispatch(ctx, cmd, args)
	if notice != nil && !quiet {
		select {
		case n, ok := <-notice:
			if ok && n != "" {
				fmt.Fprintln(env.Err, yellow.Render("↑ ")+n)
			}
		case <-time.After(300 * time.Millisecond):
		}
	}
	code := exitCode(err)
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(env.Err, faint.Render("Stopped."))
	case errors.Is(err, errSilent):
	case errors.Is(err, errBack), errors.Is(err, huh.ErrUserAborted):
		fmt.Fprintln(env.Err, faint.Render("Cancelled: nothing was saved."))
	default:
		fmt.Fprintln(env.Err, red.Render("✗ ")+err.Error())
	}
	return code
}

// offlineCommand: commands that never talk to the MKTAuto server (so a bad server address doesn't stop them, e.g.
// `mktauto update` still works).
func offlineCommand(cmd string) bool {
	switch cmd {
	case "", "help", "-h", "--help", "version", "--version", "-v", "update", "completion", "__complete":
		return true
	}
	return false
}

func newEnv(version string) (*Env, error) {
	cfg, err := auth.LoadConfig()
	if err != nil {
		return nil, err
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	interactive := term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) && os.Getenv("MKTAUTO_NO_TUI") == ""
	return &Env{Version: version, Out: os.Stdout, Data: os.Stdout, Err: os.Stderr, In: os.Stdin, Interactive: interactive, Config: cfg,
		Store: auth.NewTokenStore(), Dir: dir}, nil
}

func (e *Env) dispatch(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "":
		return e.menu(ctx)
	case "post":
		return e.post(ctx, args)
	case "login":
		return e.login(ctx, args)
	case "logout":
		return e.logout(ctx)
	case "whoami":
		return e.whoami(ctx)
	case "status":
		return e.status(ctx, args)
	case "retry":
		return e.retry(ctx, args)
	case "history":
		return e.history(args)
	case "open":
		return e.open(ctx, args)
	case "doctor":
		return e.doctor(ctx)
	case "projects":
		return e.projects(ctx, args)
	case "accounts":
		return e.accounts(ctx, args)
	case "boards":
		return e.boards(ctx, args)
	case "schedule", "reschedule":
		return e.schedule(ctx, args)
	case "unschedule":
		return e.unschedule(ctx, args)
	case "publish":
		return e.publish(ctx, args)
	case "cancel":
		return e.cancel(ctx, args)
	case "delete":
		return e.deletePost(ctx, args)
	case "edit":
		return e.edit(ctx, args)
	case "ai":
		return e.aiCommand(ctx, args)
	case "posts":
		return e.posts(ctx, args)
	case "batch":
		return e.batch(ctx, args)
	case "draft":
		return e.draft(ctx, args)
	case "calendar":
		return e.calendar(ctx, args)
	case "stats":
		return e.stats(ctx, args)
	case "balance", "credit", "wallet":
		return e.balance(ctx, args)
	case "release-summary":
		return e.releaseSummary(ctx, args)
	case "billing":
		return e.billing(ctx, args)
	case "inbox":
		return e.inbox(ctx, args)
	case "duplicate", "repost":
		return e.duplicate(ctx, args)
	case "stop-repeat":
		return e.stopRepeat(ctx, args)
	case "completion":
		return e.completion(args)
	case "watch":
		return e.watch(ctx, args)
	case "mcp":
		return e.mcp(ctx, args)
	case "best-times", "besttimes":
		return e.bestTimes(ctx, args)
	case "preset", "presets":
		return e.preset(args)
	case "__complete":
		return e.completeCommand(ctx, args)
	case "import":
		return e.importCSV(ctx, args)
	case "update":
		return e.update(ctx, args)
	case "version", "--version", "-v":
		fmt.Fprintln(e.Out, "mktauto", e.Version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(e.Out, usage)
		return nil
	}
	fmt.Fprint(e.Err, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

// token: MKTAUTO_TOKEN (for scripts), else the stored one.
func (e *Env) token() (string, error) {
	if t := os.Getenv("MKTAUTO_TOKEN"); t != "" {
		return t, nil
	}
	return e.Store.Get(e.Server)
}

// client is signed in, or says how to sign in.
func (e *Env) client() (*api.Client, error) {
	t, err := e.token()
	if err != nil || t == "" {
		return nil, withCode(exitAuth, fmt.Errorf("not signed in to %s: run `mktauto login`", e.Server))
	}
	return api.New(e.Server, t, e.Version), nil
}

// me checks the session; a revoked or expired one is forgotten.
func (e *Env) me(ctx context.Context, c *api.Client) (*api.Me, error) {
	m, err := c.Me(ctx)
	if err == nil {
		return m, nil
	}
	if api.IsProblem(err, 401, "") {
		if os.Getenv("MKTAUTO_TOKEN") == "" {
			_ = e.Store.Delete(e.Server)
		}
		return nil, withCode(exitAuth, errors.New("this terminal was signed out (revoked or expired). Run `mktauto login` to sign in again"))
	}
	if api.IsProblem(err, 426, "") {
		return nil, fmt.Errorf("%v", err)
	}
	return nil, err
}

func (e *Env) printf(format string, a ...any) { fmt.Fprintf(e.Out, format, a...) }

func plural(n int, one string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %ss", n, one)
}
