package app

import (
	"fmt"
	"strings"

	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
	"github.com/charmbracelet/huh"
)

// Alt text per image (ADR-080): a short description for people who can't see it. X, Instagram, Threads, Facebook Pages
// and Pinterest show it; typed, or written by the server's AI (which looks at the image) for the user to check.

func (p *poster) images() []media.File {
	var out []media.File
	for _, f := range p.files {
		if !f.IsVideo() {
			out = append(out, f)
		}
	}
	return out
}

// aiAlt asks the AI for one image (it must be uploaded already).
func (p *poster) aiAlt(f media.File) (string, error) {
	m, ok := p.uploaded[f.Path]
	if !ok {
		return "", fmt.Errorf("%s isn't uploaded", f.Name)
	}
	var text string
	err := p.spin("Writing alt text for "+f.Name+"…", func() error {
		res, err := p.c.AIAltText(p.ctx, m.ID, "")
		if err == nil {
			text = res.AltText
			if p.aiStatus != nil {
				p.aiStatus.UsedToday, p.aiStatus.DailyLimit = res.UsedToday, res.DailyLimit
			}
		}
		return err
	})
	return text, apiDetail(err)
}

func (p *poster) altTextStep() error {
	imgs := p.images()
	if len(imgs) == 0 {
		return nil
	}
	if !p.wizard {
		for _, spec := range p.f.alt {
			name, text, ok := strings.Cut(spec, "=")
			if !ok {
				return fmt.Errorf("--alt %q: write FILE=TEXT", spec)
			}
			found := false
			for _, f := range imgs {
				if f.Name == strings.TrimSpace(name) || f.Path == strings.TrimSpace(name) {
					p.plan.AltTexts[f.Path], found = strings.TrimSpace(text), true
				}
			}
			if !found {
				return fmt.Errorf("--alt: %s isn't one of the post's images", name)
			}
		}
		if p.f.altAI && !p.f.dryRun {
			for _, f := range imgs {
				if p.plan.AltTexts[f.Path] != "" {
					continue
				}
				text, err := p.aiAlt(f)
				if err != nil {
					return fmt.Errorf("AI alt text for %s: %w", f.Name, err)
				}
				p.plan.AltTexts[f.Path] = text
				p.env.printf("%s %s: %s\n", faint.Render("Alt text"), f.Name, text)
			}
		}
		return nil
	}
	if p.altMode == "" {
		p.altMode = "none"
		for _, f := range imgs {
			if p.plan.AltTexts[f.Path] != "" {
				p.altMode = "write"
			}
		}
	}
	opts := []huh.Option[string]{huh.NewOption("Skip", "none"), huh.NewOption("Write it myself", "write")}
	canAI := !p.f.dryRun && p.aiReady(p.ctx)
	if canAI {
		opts = append(opts, huh.NewOption("Let AI write it, then I check it", "ai"))
	}
	steps := []step{{run: func() error {
		return p.ask.form(huh.NewSelect[string]().Title(fmt.Sprintf("Alt text for the %s", plural(len(imgs), "image"))).
			Description("A short description for people who can't see the image. X, Instagram, Threads, Facebook Pages and Pinterest use it.").
			Options(opts...).Value(&p.altMode))
	}}}
	for _, f := range imgs {
		steps = append(steps, step{run: func() error {
			if p.altMode == "none" {
				return nil
			}
			text := p.plan.AltTexts[f.Path]
			if text == "" && p.altMode == "ai" && !p.ask.replaying() {
				var err error
				if text, err = p.aiAlt(f); err != nil {
					p.env.printf("%s %s: %v\n", red.Render("✗"), f.Name, err)
				}
			}
			count := func() string {
				n := len([]rune(text))
				s := fmt.Sprintf("%d / 1,000 characters", n)
				if n > 500 {
					s += yellow.Render(" · Pinterest takes 500")
				}
				if p.aiStatus != nil && p.altMode == "ai" {
					s += " · " + p.aiUsage()
				}
				return s + " · empty = none"
			}
			if err := p.ask.form(huh.NewText().Title("Alt text: "+p.fileLabel(f)).DescriptionFunc(count, &text).Value(&text).Lines(3).CharLimit(1000)); err != nil {
				return err
			}
			p.plan.AltTexts[f.Path] = strings.TrimSpace(text)
			return nil
		}})
	}
	if err := runSteps(p.ask, steps, false); err != nil {
		return err
	}
	if p.altMode == "none" {
		for _, f := range imgs {
			delete(p.plan.AltTexts, f.Path)
		}
	}
	return nil
}

// threadOf: an own caption with "---" lines on X or Threads is a thread: the first part is the post, the rest replies.
func threadOf(platform, text string) (string, []string) {
	parts := compose.SplitParts(text)
	if !compose.ThreadPlatform(platform) || len(parts) < 2 {
		return strings.TrimSpace(text), nil
	}
	return parts[0], parts[1:]
}
