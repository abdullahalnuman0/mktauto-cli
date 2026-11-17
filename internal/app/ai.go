package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/abdullahalnuman0/mktauto-cli/internal/ui"
	"github.com/charmbracelet/huh"
)

// AI caption help through the server's helper (ADR-035, ADR-077): the user's own daily AI limit applies.

// aiReady: the helper is set up on the server (asked once per run).
func (p *poster) aiReady(ctx context.Context) bool {
	if p.aiChecked {
		return p.aiStatus != nil && p.aiStatus.Enabled
	}
	p.aiChecked = true
	s, err := p.c.AIStatus(ctx)
	if err == nil {
		p.aiStatus = s
	}
	return p.aiStatus != nil && p.aiStatus.Enabled
}

func (p *poster) aiUsage() string {
	if p.aiStatus == nil || p.aiStatus.DailyLimit == 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d AI requests used today", p.aiStatus.UsedToday, p.aiStatus.DailyLimit)
}

// spin runs work with a spinner in a terminal.
func (p *poster) spin(title string, work func() error) error {
	return ui.Spin(title, p.env.Interactive, work)
}

// apiDetail: the server's own words for a refusal.
func apiDetail(err error) error {
	var prob *api.Problem
	if errors.As(err, &prob) && prob.Detail != "" {
		return errors.New(prob.Detail)
	}
	return err
}

// askAI calls the helper with a spinner and remembers the day's usage.
func (p *poster) askAI(ctx context.Context, r api.AIRequest) (*api.AIResult, error) {
	var res *api.AIResult
	err := ui.Spin("Asking AI…", p.env.Interactive, func() (err error) {
		res, err = p.c.AICaption(ctx, r)
		return err
	})
	if err != nil {
		var prob *api.Problem
		if errors.As(err, &prob) && prob.Detail != "" {
			return nil, errors.New(prob.Detail)
		}
		return nil, err
	}
	if p.aiStatus != nil {
		p.aiStatus.UsedToday, p.aiStatus.DailyLimit = res.UsedToday, res.DailyLimit
	}
	return res, nil
}

// pickSuggestion shows the versions in full and asks which one to use ("" = none).
func (p *poster) pickSuggestion(title string, suggestions []string) (string, error) {
	var b strings.Builder
	opts := []huh.Option[int]{}
	for i, s := range suggestions {
		fmt.Fprintf(&b, "%s %s\n\n", brand.Render(fmt.Sprintf("%d.", i+1)), s)
		opts = append(opts, huh.NewOption(fmt.Sprintf("Use version %d", i+1), i))
	}
	opts = append(opts, huh.NewOption("None of them", -1))
	pick := 0
	back, err := p.ask.run(huh.NewForm(huh.NewGroup(
		huh.NewNote().Title(title).Description(strings.TrimRight(b.String(), "\n")),
		huh.NewSelect[int]().Options(opts...).Value(&pick),
	)).WithShowHelp(true))
	if err != nil {
		return "", err
	}
	if back || pick < 0 {
		return "", nil
	}
	return suggestions[pick], nil
}

// shortestLimit: the chosen accounts' smallest text limit and its platform (0 when none is small).
func (p *poster) shortestLimit() (int, string) {
	best, platform := 0, ""
	for _, a := range p.plan.Accounts {
		if l := compose.MaxText(a.Platform, p.plan.FormatFor(a)); l > 0 && l < 10000 && (best == 0 || l < best) {
			best, platform = l, a.Platform
		}
	}
	return best, platform
}

// aiHelp: after the texts screen, when "AI caption help" was ticked. A menu of what the helper can do; each result
// can be taken or left. Counts as one question for going back.
func (p *poster) aiHelp(ctx context.Context) error {
	if !p.wizard || !p.wantAI {
		return nil
	}
	start := p.ask.asked
	if p.ask.replaying() {
		p.ask.skip--
		p.ask.asked++
		return nil
	}
	defer func() { p.ask.asked = start + 1 }()
	plan := p.plan
	limit, limitPlatform := p.shortestLimit()
	for {
		has := strings.TrimSpace(plan.Caption) != ""
		opts := []huh.Option[string]{huh.NewOption("Done: continue with this caption", "done")}
		if has {
			opts = append(opts, huh.NewOption("Improve it (3 versions)", "IMPROVE"))
			if limit > 0 {
				opts = append(opts, huh.NewOption(fmt.Sprintf("Shorter: fit %s (%d characters)", compose.PlatformLabel[limitPlatform], limit), "SHORTEN"))
			}
			opts = append(opts, huh.NewOption("More professional", "PROFESSIONAL"), huh.NewOption("More casual and friendly", "CASUAL"),
				huh.NewOption("Suggest hashtags", "HASHTAGS"), huh.NewOption("Translate…", "TRANSLATE"))
		} else {
			opts = append(opts, huh.NewOption("Write a caption from a few words", "WRITE"))
		}
		desc := faint.Render(p.aiUsage())
		if has {
			desc = truncate(strings.ReplaceAll(strings.TrimSpace(plan.Caption), "\n", " "), 160) + "\n" + desc
		}
		what := "done"
		back, err := p.ask.run(huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("AI caption help").Description(desc).
			Options(opts...).Value(&what))).WithShowHelp(true))
		if err != nil {
			return err
		}
		if back {
			p.ask.asked = start
			return errBack
		}
		if what == "done" {
			return nil
		}
		req := api.AIRequest{Action: what, Text: plan.Caption, Platform: p.mainPlatform()}
		switch what {
		case "WRITE":
			about := ""
			if back, err := p.ask.run(huh.NewForm(huh.NewGroup(huh.NewText().Title("What is the post about?").
				Description("a few words or sentences: the AI writes 3 captions from them").Lines(3).Value(&about))).WithShowHelp(true)); err != nil {
				return err
			} else if back || strings.TrimSpace(about) == "" {
				continue
			}
			req.Action, req.Text = "IMPROVE", about
		case "SHORTEN":
			req.Platform, req.MaxLength = limitPlatform, max(40, limit-p.finalLength(limitPlatform, "")-5)
		case "TRANSLATE":
			lang := "English"
			if back, err := p.ask.run(huh.NewForm(huh.NewGroup(huh.NewInput().Title("Into which language?").Value(&lang))).WithShowHelp(true)); err != nil {
				return err
			} else if back {
				continue
			}
			req.Language = lang
		}
		res, err := p.askAI(ctx, req)
		if err != nil {
			p.env.printf("%s %v\n", red.Render("✗"), err)
			continue
		}
		if what == "HASHTAGS" {
			p.addSuggestedHashtags(res.Hashtags)
			continue
		}
		if pick, err := p.pickSuggestion("AI suggestions", res.Suggestions); err != nil {
			return err
		} else if pick != "" {
			plan.Caption = pick
		}
	}
}

// mainPlatform: the platform the caption is written for (the only one, else general).
func (p *poster) mainPlatform() string {
	if len(p.plan.Accounts) == 1 {
		return p.plan.Accounts[0].Platform
	}
	return ""
}

func (p *poster) addSuggestedHashtags(tags []string) {
	if len(tags) == 0 {
		p.env.printf("%s\n", faint.Render("No hashtags suggested."))
		return
	}
	opts := make([]huh.Option[string], len(tags))
	for i, t := range tags {
		opts[i] = huh.NewOption("#"+t, t).Selected(i < 8)
	}
	var picked []string
	if back, err := p.ask.run(huh.NewForm(huh.NewGroup(huh.NewMultiSelect[string]().Title("Add these hashtags").
		Options(opts...).Value(&picked))).WithShowHelp(true)); err != nil || back || len(picked) == 0 {
		return
	}
	p.hashtags = strings.TrimSpace(p.hashtags + " " + strings.Join(picked, " "))
	if tags, err := p.hashtagsWithSets(); err == nil {
		p.plan.Hashtags = tags
	}
}

// aiShortenFor: versions of the main caption that fit the account (own caption "let AI shorten it").
func (p *poster) aiShortenFor(ctx context.Context, a api.ProjectAccount, limit int) (string, error) {
	res, err := p.askAI(ctx, api.AIRequest{Action: "SHORTEN", Text: p.plan.Caption, Platform: a.Platform,
		MaxLength: max(40, limit-p.finalLength(a.Platform, "")-5)})
	if err != nil {
		return "", err
	}
	return p.pickSuggestion("Shorter for "+accountLabel(a), res.Suggestions)
}

// ------------------------------------------------------------------ mktauto ai

func (e *Env) aiCommand(ctx context.Context, args []string) error {
	fs := e.flags("ai", "ai ACTION [TEXT] [--file FILE] [--platform P] [--max N] [--language L] [--json]\n"+
		"  ACTION: improve, shorten, professional, casual, hashtags or translate · TEXT or --file (or stdin with -)")
	file := fs.String("file", "", "read the caption from this file")
	platform := fs.String("platform", "", "write it for this platform (instagram, x, tiktok…)")
	maxLen := fs.Int("max", 0, "shorten: at most this many characters")
	lang := fs.String("language", "English", "translate: into this language")
	asJSON := fs.Bool("json", false, "print JSON")
	var action string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	var text string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		text, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if text == "" {
		text = strings.Join(fs.Args(), " ")
	}
	action = strings.ToUpper(action)
	valid := map[string]bool{"IMPROVE": true, "SHORTEN": true, "PROFESSIONAL": true, "CASUAL": true, "HASHTAGS": true, "TRANSLATE": true}
	if !valid[action] {
		fs.Usage()
		return fmt.Errorf("unknown action %q", strings.ToLower(action))
	}
	switch {
	case *file != "":
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		text = string(data)
	case text == "-":
		data, err := io.ReadAll(e.In)
		if err != nil {
			return err
		}
		text = string(data)
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("give the caption as TEXT, --file FILE or - (stdin)")
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	p := &poster{env: e, c: c}
	pl := strings.ToUpper(*platform)
	if pl == "FACEBOOK" {
		pl = "FACEBOOK_PAGE"
	}
	res, err := p.askAI(ctx, api.AIRequest{Action: action, Text: text, Platform: pl, MaxLength: *maxLen, Language: *lang})
	if err != nil {
		return err
	}
	if *asJSON {
		return e.printJSON(res)
	}
	for i, s := range res.Suggestions {
		e.printf("%s %s\n\n", brand.Render(fmt.Sprintf("%d.", i+1)), s)
	}
	if len(res.Hashtags) > 0 {
		e.printf("#%s\n", strings.Join(res.Hashtags, " #"))
	}
	e.printf("%s\n", faint.Render(fmt.Sprintf("%d of %d AI requests used today", res.UsedToday, res.DailyLimit)))
	return nil
}
