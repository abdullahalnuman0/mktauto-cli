package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/auth"
	"github.com/charmbracelet/huh"
)

func (e *Env) login(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	code := fs.String("code", "", "a one-time login code from Settings → Terminal access (instead of browser approval)")
	noBrowser := fs.Bool("no-browser", false, "don't open the browser; just show the address and the code")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c := api.New(e.Server, "", e.Version)
	device := auth.ThisDevice(e.Version)
	var tok *api.SessionToken
	var err error
	if *code == "" && e.Interactive {
		method := "browser"
		if err := huh.NewSelect[string]().Title("How do you want to sign in to " + e.Server + "?").
			Options(huh.NewOption("Approve in the browser (recommended)", "browser"),
				huh.NewOption("Paste a login code from Settings → Terminal access", "code")).
			Value(&method).Run(); err != nil {
			return err
		}
		if method == "code" {
			if err := huh.NewInput().Title("Login code").Placeholder("mkl_…").EchoMode(huh.EchoModePassword).
				Validate(func(s string) error {
					if !strings.HasPrefix(strings.TrimSpace(s), "mkl_") {
						return errors.New("a login code starts with mkl_")
					}
					return nil
				}).Value(code).Run(); err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(*code) != "" {
		tok, err = c.Redeem(ctx, strings.TrimSpace(*code), device)
		if api.IsProblem(err, 401, "") {
			return errors.New("that login code is wrong, already used, expired or revoked: create a new one in Settings → Terminal access")
		}
	} else {
		tok, err = e.browserLogin(ctx, c, device, *noBrowser)
	}
	if err != nil {
		return err
	}
	if err := e.Store.Set(e.Server, tok.Token); err != nil {
		return fmt.Errorf("signed in, but couldn't save the session: %w", err)
	}
	if e.Config.Server != e.Server {
		e.Config.Server = e.Server
		_ = e.Config.Save()
	}
	s := tok.Session
	e.printf("%s Signed in as %s on %s\n", green.Render("✓"), bold.Render(s.Name), e.Server)
	e.printf("  %s · stays signed in until %s\n", permissionText(s.Permission), s.ExpiresAt.Local().Format("2 Jan 2006"))
	e.printf("  Next: open a folder with images or videos and run %s\n", bold.Render("mktauto"))
	return nil
}

// browserLogin: show a short code, open the approval page, wait until it's approved on the website.
func (e *Env) browserLogin(ctx context.Context, c *api.Client, device api.Device, noBrowser bool) (*api.SessionToken, error) {
	start, err := c.StartDeviceAuth(ctx, device)
	if err != nil {
		return nil, err
	}
	e.printf("\nYour code: %s\n", brand.Render(start.UserCode))
	opened := !noBrowser && auth.OpenBrowser(start.VerificationURLComplete)
	if opened {
		e.printf("A browser window opened. Check that it shows the same code, then approve.\n")
	}
	e.printf("%s %s\n", faint.Render("Or open:"), start.VerificationURLComplete)
	e.printf("%s\n", faint.Render("Waiting for approval (Ctrl+C to stop)…"))
	interval := time.Duration(max(start.Interval, 2)) * time.Second
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		r, err := c.Poll(ctx, start.DeviceCode)
		switch {
		case err == nil && r.Session != nil:
			return r.Session, nil
		case err == nil:
			if r.Interval > 0 {
				interval = time.Duration(r.Interval) * time.Second
			}
		case api.IsProblem(err, 429, ""):
			interval += time.Second
		case api.IsProblem(err, 403, ""):
			return nil, errors.New("the sign-in was denied on the website")
		case api.IsProblem(err, 410, ""):
			return nil, errors.New("the sign-in request expired: run `mktauto login` again")
		default:
			return nil, err
		}
	}
	return nil, errors.New("nobody approved the sign-in within 10 minutes: run `mktauto login` again")
}

func (e *Env) logout(ctx context.Context) error {
	t, err := e.Store.Get(e.Server)
	if err != nil || t == "" {
		e.printf("Not signed in to %s.\n", e.Server)
		return nil
	}
	// Tell the server first, so the session can't be used again even if a copy of the token exists.
	if err := api.New(e.Server, t, e.Version).Logout(ctx); err != nil && !api.IsProblem(err, 401, "") {
		e.printf("%s couldn't reach the server (%v): the session is forgotten here; revoke it in Settings → Terminal access.\n", yellow.Render("!"), err)
	}
	if err := e.Store.Delete(e.Server); err != nil {
		return err
	}
	e.printf("%s Signed out of %s\n", green.Render("✓"), e.Server)
	return nil
}

func permissionText(p string) string {
	switch p {
	case "DRAFT":
		return "May save drafts"
	case "SCHEDULE":
		return "May save drafts and schedule"
	case "PUBLISH":
		return "May save drafts, schedule and publish now"
	}
	return p
}
