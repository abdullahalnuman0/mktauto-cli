package app

import (
	"context"
	"errors"
	"flag"
	"strings"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/charmbracelet/huh"
)

// Exit codes for scripts (ADR-079); `mktauto help` lists them.
const (
	exitOK          = 0
	exitFailed      = 1
	exitUsage       = 2   // wrong command line
	exitAuth        = 3   // not signed in, or the terminal was signed out
	exitNotAllowed  = 4   // this terminal's permission or projects don't allow it
	exitInvalid     = 5   // the post can't be published as it is (saved as a draft)
	exitLimit       = 6   // a limit was reached (daily posts, requests, AI)
	exitTooOld      = 7   // this version is too old for the server: mktauto update
	exitInterrupted = 130 // Ctrl+C or cancelled
)

const exitCodesHelp = `Exit codes: 0 done · 1 failed · 2 wrong command line · 3 not signed in · 4 not allowed for this terminal ·
5 the post can't be published as it is · 6 a limit was reached · 7 update mktauto · 130 cancelled
`

// codedError carries an exit code.
type codedError struct {
	code int
	err  error
}

func (c *codedError) Error() string { return c.err.Error() }
func (c *codedError) Unwrap() error { return c.err }

func withCode(code int, err error) error { return &codedError{code, err} }

func exitCode(err error) int {
	var ce *codedError
	var prob *api.Problem
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.As(err, &ce):
		return ce.code
	case errors.Is(err, context.Canceled), errors.Is(err, errBack), errors.Is(err, huh.ErrUserAborted):
		return exitInterrupted
	case isFlagError(err):
		return exitUsage
	case errors.As(err, &prob):
		switch {
		case prob.Status == 401:
			return exitAuth
		case prob.Status == 403:
			return exitNotAllowed
		case prob.Status == 422 || prob.Validation != nil:
			return exitInvalid
		case prob.Status == 429:
			return exitLimit
		case prob.Status == 426:
			return exitTooOld
		}
	}
	return exitFailed
}

// isFlagError: the messages Go's flag package returns for a wrong command line.
func isFlagError(err error) bool {
	m := err.Error()
	for _, p := range []string{"flag provided but not defined", "invalid value", "flag needs an argument", "invalid boolean", "bad flag syntax"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}
