package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/charmbracelet/huh"
)

// Templates, hashtag sets and account groups saved on the website (ADR-077).

// loadPresets reads them once; an older server or none saved simply means none are offered.
func (p *poster) loadPresets(ctx context.Context) {
	if p.presetsLoaded {
		return
	}
	p.presetsLoaded = true
	p.templates, _ = p.c.Templates(ctx)
	p.sets, _ = p.c.HashtagSets(ctx)
	p.groups, _ = p.c.AccountGroups(ctx)
}

func findByName[T any](list []T, term string, id, name func(T) string) (*T, error) {
	for i, x := range list {
		if strings.EqualFold(id(x), term) || strings.EqualFold(name(x), term) {
			return &list[i], nil
		}
	}
	names := make([]string, len(list))
	for i, x := range list {
		names[i] = name(x)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%q not found (none saved on the website)", term)
	}
	return nil, fmt.Errorf("%q not found (one of: %s)", term, strings.Join(names, ", "))
}

func tplID(t api.Template) string       { return t.ID }
func tplName(t api.Template) string     { return t.Name }
func setID(s api.HashtagSet) string     { return s.ID }
func setName(s api.HashtagSet) string   { return s.Name }
func groupID(g api.AccountGroup) string { return g.ID }
func groupName(g api.AccountGroup) string {
	return g.Name
}

// chooseTemplate: start from a saved template (its caption, hashtags, link, first comment and accounts).
func (p *poster) chooseTemplate(ctx context.Context) error {
	p.loadPresets(ctx)
	if !p.wizard {
		if p.f.template == "" {
			return nil
		}
		t, err := findByName(p.templates, p.f.template, tplID, tplName)
		if err != nil {
			return fmt.Errorf("--template: %w", err)
		}
		p.template = t
		return nil
	}
	if len(p.templates) == 0 {
		return nil
	}
	id := ""
	if p.template != nil {
		id = p.template.ID
	}
	opts := []huh.Option[string]{huh.NewOption("No template", "")}
	for _, t := range p.templates {
		label := t.Name
		if c := strings.TrimSpace(strings.ReplaceAll(t.Caption, "\n", " ")); c != "" {
			label += faint.Render("  " + truncate(c, 50))
		}
		opts = append(opts, huh.NewOption(label, t.ID))
	}
	if err := p.ask.form(huh.NewSelect[string]().Title("Start from a template?").Description("caption, hashtags, link, first comment and accounts saved on the website · / to search").
		Options(opts...).Value(&id).Height(min(len(opts)+2, 14))); err != nil {
		return err
	}
	p.template = nil
	if id != "" {
		p.template, _ = findByName(p.templates, id, tplID, tplName)
	}
	return nil
}

// applyTemplate fills in what the template has, without overwriting what was typed; switching templates swaps the
// values that still are the old template's.
func (p *poster) applyTemplate() {
	newID := ""
	var t api.Template
	if p.template != nil {
		t, newID = *p.template, p.template.ID
	}
	if newID == p.appliedID {
		return
	}
	old := p.applied
	swap := func(cur *string, oldV, newV string) {
		if strings.TrimSpace(*cur) == "" || (old != nil && *cur == oldV) {
			*cur = newV
		}
	}
	var o api.Template
	if old != nil {
		o = *old
	}
	swap(&p.plan.Caption, o.Caption, t.Caption)
	swap(&p.hashtags, strings.Join(o.Hashtags, " "), strings.Join(t.Hashtags, " "))
	swap(&p.plan.Link, o.LinkURL, t.LinkURL)
	swap(&p.plan.FirstComment, o.FirstComment, t.FirstComment)
	p.appliedID, p.applied = newID, p.template
}

// expandGroups turns "group:NAME" terms into the group's account ids.
func (p *poster) expandGroups(terms []string) ([]string, error) {
	var out []string
	for _, term := range terms {
		name, ok := strings.CutPrefix(term, "group:")
		if !ok {
			out = append(out, term)
			continue
		}
		g, err := findByName(p.groups, name, groupID, groupName)
		if err != nil {
			return nil, fmt.Errorf("account group %w", err)
		}
		out = append(out, g.AccountIDs...)
	}
	return out, nil
}

// hashtagsWithSets: the typed hashtags, then the chosen sets' tags (each once).
func (p *poster) hashtagsWithSets() ([]string, error) {
	tags := compose.ParseHashtags(p.hashtags)
	ids := slices.Clone(p.setIDs)
	for _, name := range splitList(p.f.hashtagSet) {
		s, err := findByName(p.sets, name, setID, setName)
		if err != nil {
			return nil, fmt.Errorf("--hashtag-set: %w", err)
		}
		ids = append(ids, s.ID)
	}
	seen := map[string]bool{}
	for _, t := range tags {
		seen[strings.ToLower(t)] = true
	}
	for _, id := range ids {
		for _, s := range p.sets {
			if s.ID != id {
				continue
			}
			for _, t := range compose.ParseHashtags(strings.Join(s.Tags, " ")) {
				if !seen[strings.ToLower(t)] {
					seen[strings.ToLower(t)] = true
					tags = append(tags, t)
				}
			}
		}
	}
	return tags, nil
}
