package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/update"
)

// releases is replaceable in tests.
var releases = update.GitHub

func (e *Env) update(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	version := fs.String("version", "", "install this version (e.g. v1.2.0), also to go back to an older one")
	check := fs.Bool("check", false, "only say whether a newer version exists")
	if err := fs.Parse(args); err != nil {
		return err
	}
	src := releases()
	target := *version
	if target == "" {
		latest, err := src.Latest(ctx)
		if err != nil {
			return fmt.Errorf("can't find the latest version: %w", err)
		}
		target = latest
		e.Config.LatestVersion, e.Config.LastUpdateCheck = latest, time.Now().UTC().Format(time.RFC3339)
		_ = e.Config.Save()
		if !update.Newer(latest, e.Version) && e.Version != "dev" {
			e.printf("%s mktauto %s is the latest version.\n", green.Render("✓"), e.Version)
			return nil
		}
	}
	if *check {
		e.printf("mktauto %s is available (you have %s). Run: mktauto update\n", target, e.Version)
		return nil
	}
	if target[0] != 'v' {
		target = "v" + target
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	e.printf("Downloading mktauto %s for %s/%s…\n", target, runtime.GOOS, runtime.GOARCH)
	program, err := src.Fetch(ctx, target, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := update.Replace(exe, program); err != nil {
		return err
	}
	e.printf("%s Updated to %s (signature and checksum verified): %s\n", green.Render("✓"), target, exe)
	return nil
}

// noticeUpdate checks GitHub at most once a day (2 s limit, silent when offline) and returns a one-line notice when a
// newer version exists. MKTAUTO_NO_UPDATE_CHECK=1 turns it off.
func (e *Env) noticeUpdate(ctx context.Context) <-chan string {
	out := make(chan string, 1)
	if e.Version == "dev" || os.Getenv("MKTAUTO_NO_UPDATE_CHECK") != "" {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		latest := e.Config.LatestVersion
		last, _ := time.Parse(time.RFC3339, e.Config.LastUpdateCheck)
		if time.Since(last) > 24*time.Hour {
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			v, err := releases().Latest(cctx)
			if err != nil {
				return
			}
			latest = v
			e.Config.LatestVersion, e.Config.LastUpdateCheck = v, time.Now().UTC().Format(time.RFC3339)
			_ = e.Config.Save()
		}
		if latest != "" && update.Newer(latest, e.Version) {
			out <- fmt.Sprintf("mktauto %s is available (you have %s): run `mktauto update`", latest, e.Version)
		}
	}()
	return out
}

