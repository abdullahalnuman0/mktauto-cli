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
	In          *os.File
	Interactive bool
	Config      *auth.Config
	Store       auth.TokenStore
	Server      string
	Dir         string // the folder mktauto runs in
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
  mktauto                 menu (signs in first when needed)
  mktauto post [flags]    post files from this folder (no flags: step by step)
  mktauto login           sign in (browser approval; --code for a login code)
  mktauto logout          sign out this computer
  mktauto whoami          who and what this terminal may do
  mktauto status [post]   recent posts, or one post's accounts
  mktauto retry <post>    publish failed accounts of a post again
  mktauto history [--all] what was posted from this folder
  mktauto open [post]     open the website (or a post) in the browser
  mktauto doctor          check the connection and this installation
  mktauto update          install the latest version (--version vX.Y.Z for another one)
  mktauto version

Global: --server URL (or MKTAUTO_SERVER) to use another MKTAuto server.
Run "mktauto <command> -h" for a command's flags. More: https://mktauto.site/download
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
	// --server may come before the command.
	server := ""
	for len(args) > 0 && strings.HasPrefix(args[0], "--server") {
		if v, ok := strings.CutPrefix(args[0], "--server="); ok {
			server, args = v, args[1:]
		} else if len(args) > 1 {
			server, args = args[1], args[2:]
		} else {
			args = args[1:]
		}
	}
	env.Server = api.NormalizeServer(env.Config.ServerURL(server))
	cmd := ""
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if exe, err := os.Executable(); err == nil {
		update.Cleanup(exe) // the program a Windows update left behind
	}
	var notice <-chan string
	if cmd != "update" && cmd != "version" && cmd != "--version" && cmd != "-v" && cmd != "help" && cmd != "-h" && cmd != "--help" {
		notice = env.noticeUpdate(ctx)
	}
	err = env.dispatch(ctx, cmd, args)
	if notice != nil {
		select {
		case n, ok := <-notice:
			if ok && n != "" {
				fmt.Fprintln(env.Err, yellow.Render("↑ ")+n)
			}
		case <-time.After(300 * time.Millisecond):
		}
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(env.Err, faint.Render("Stopped."))
		return 130
	case errors.Is(err, errSilent):
		return 1
	case errors.Is(err, errBack), errors.Is(err, huh.ErrUserAborted):
		fmt.Fprintln(env.Err, faint.Render("Cancelled: nothing was saved."))
		return 130
	}
	fmt.Fprintln(env.Err, red.Render("✗ ")+err.Error())
	return 1
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
	return &Env{Version: version, Out: os.Stdout, Err: os.Stderr, In: os.Stdin, Interactive: interactive, Config: cfg,
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
		return nil, fmt.Errorf("not signed in to %s: run `mktauto login`", e.Server)
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
		return nil, errors.New("this terminal was signed out (revoked or expired). Run `mktauto login` to sign in again")
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
