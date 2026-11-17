package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// mktauto posts: a full-screen list of recent posts with their details and one-key actions (ADR-077).

var dashTabs = []struct {
	name string
	in   func(api.Post) bool
}{
	{"All", func(api.Post) bool { return true }},
	{"Drafts", func(p api.Post) bool { return p.Status == "DRAFT" }},
	{"Scheduled", func(p api.Post) bool { return p.Status == "SCHEDULED" || p.Status == "PENDING_APPROVAL" }},
	{"Published", func(p api.Post) bool { return p.Status == "COMPLETED" || p.Status == "PUBLISHING" }},
	{"Problems", func(p api.Post) bool {
		if p.Status == "FAILED" || p.Status == "PARTIALLY_FAILED" {
			return true
		}
		for _, d := range p.Destinations {
			if d.Status == "FAILED" || d.Status == "NEEDS_VERIFICATION" || d.Status == "MANUAL_REQUIRED" {
				return true
			}
		}
		return false
	}},
}

type dashItem struct{ p api.Post }

func (i dashItem) FilterValue() string {
	var accs []string
	for _, d := range i.p.Destinations {
		accs = append(accs, d.AccountName, compose.PlatformLabel[d.Platform])
	}
	return i.p.Title() + " " + i.p.BaseCaption + " " + i.p.ID + " " + strings.Join(accs, " ")
}

func (i dashItem) Title() string { return truncate(i.p.Title(), 60) }

func (i dashItem) Description() string {
	when := "not scheduled"
	if i.p.ScheduledAt != nil {
		when = i.p.ScheduledAt.Local().Format("Mon 2 Jan 15:04")
	}
	platforms := map[string]bool{}
	var names []string
	for _, d := range i.p.Destinations {
		if l := platformShort(d.Platform); !platforms[l] {
			platforms[l] = true
			names = append(names, l)
		}
	}
	return fmt.Sprintf("%s · %s · %s", statusText(i.p.Status), when, strings.Join(names, " "))
}

type dashModel struct {
	list    list.Model
	all     []api.Post
	me      *api.Me
	tab     int
	w, h    int
	msg     string
	action  string // key pressed ("" = quit, "R" = refresh)
	chosen  *api.Post
	details bool // narrow screens: details instead of the list
}

func newDash(posts []api.Post, me *api.Me, tab int, selectID, msg string) dashModel {
	d := list.NewDefaultDelegate()
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.Foreground(lipgloss.Color("63")).BorderForeground(lipgloss.Color("63"))
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.BorderForeground(lipgloss.Color("63"))
	l := list.New(nil, d, 0, 0)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetStatusBarItemName("post", "posts")
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	m := dashModel{list: l, all: posts, me: me, tab: tab, msg: msg}
	m.fill(selectID)
	return m
}

func (m *dashModel) fill(selectID string) {
	var items []list.Item
	sel := 0
	for _, p := range m.all {
		if dashTabs[m.tab].in(p) {
			if p.ID == selectID {
				sel = len(items)
			}
			items = append(items, dashItem{p})
		}
	}
	m.list.SetItems(items)
	m.list.Select(sel)
}

func (m dashModel) Init() tea.Cmd { return nil }

func (m dashModel) wide() bool { return m.w >= 110 }

func (m *dashModel) resize() {
	if m.wide() {
		m.list.SetSize(m.w*45/100, m.h-6)
	} else {
		m.list.SetSize(m.w, m.h-6)
	}
}

func (m dashModel) selected() *api.Post {
	if it, ok := m.list.SelectedItem().(dashItem); ok {
		p := it.p
		return &p
	}
	return nil
}

func (m dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.resize()
		return m, nil
	case tea.KeyMsg:
		if m.list.FilterState() == list.Filtering {
			break // typing a search
		}
		key := msg.String()
		switch key {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			if m.details {
				m.details = false
				return m, nil
			}
			if m.list.FilterState() == list.FilterApplied {
				break // clears the search
			}
			return m, tea.Quit
		case "tab", "right", "shift+tab", "left":
			if key == "tab" || key == "right" {
				m.tab = (m.tab + 1) % len(dashTabs)
			} else {
				m.tab = (m.tab + len(dashTabs) - 1) % len(dashTabs)
			}
			id := ""
			if p := m.selected(); p != nil {
				id = p.ID
			}
			m.fill(id)
			return m, nil
		case "enter":
			if !m.wide() {
				m.details = !m.details
			}
			return m, nil
		case "R":
			m.action = "R"
			return m, tea.Quit
		}
		if p := m.selected(); p != nil && len(key) == 1 {
			for _, a := range actionsFor(p, m.me) {
				if a.key == key {
					m.action, m.chosen = key, p
					return m, tea.Quit
				}
			}
			if strings.Contains("supecdro", key) {
				m.msg = yellow.Render(fmt.Sprintf("Not possible for a %s post (or not allowed for this terminal)", strings.ToLower(strings.ReplaceAll(p.Status, "_", " "))))
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m dashModel) header() string {
	var tabs []string
	for i, t := range dashTabs {
		n := 0
		for _, p := range m.all {
			if t.in(p) {
				n++
			}
		}
		label := fmt.Sprintf(" %s %d ", t.name, n)
		if i == m.tab {
			tabs = append(tabs, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("63")).Render(label))
		} else {
			tabs = append(tabs, faint.Render(label))
		}
	}
	return brand.Render("mktauto") + faint.Render(" · posts   ") + strings.Join(tabs, " ")
}

func (m dashModel) detail(width int) string {
	p := m.selected()
	if p == nil {
		return faint.Render("No posts here.")
	}
	var b strings.Builder
	b.WriteString(bold.Render(truncate(p.Title(), width)) + "\n")
	when := "not scheduled"
	if p.ScheduledAt != nil {
		when = p.ScheduledAt.Local().Format("Mon 2 Jan 2006 15:04")
	}
	b.WriteString(statusText(p.Status) + faint.Render(" · "+when+" · "+p.ID[:min(8, len(p.ID))]) + "\n\n")
	caption := strings.TrimSpace(p.BaseCaption)
	if caption == "" {
		caption = faint.Render("(no caption)")
	}
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(caption), "\n")
	if len(lines) > 8 {
		lines = append(lines[:8], faint.Render("…"))
	}
	b.WriteString(strings.Join(lines, "\n") + "\n\n")
	for _, d := range p.Destinations {
		line := fmt.Sprintf("%-10s %-20s %-9s %s", platformShort(d.Platform), truncate(d.AccountName, 20), compose.FormatLabel[d.Format], statusText(d.Status))
		b.WriteString(line + "\n")
		if d.LastErrorMessage != "" && (d.Status == "FAILED" || d.Status == "RETRY_WAIT" || d.Status == "NEEDS_VERIFICATION") {
			b.WriteString("  " + red.Render(truncate(d.LastErrorMessage, width-2)) + "\n")
		} else if d.PublishedURL != "" {
			b.WriteString("  " + faint.Render(truncate(d.PublishedURL, width-2)) + "\n")
		}
	}
	return b.String()
}

func (m dashModel) footer() string {
	var keys []string
	if p := m.selected(); p != nil {
		for _, a := range actionsFor(p, m.me) {
			keys = append(keys, brand.Render(a.key)+" "+a.label)
		}
	}
	keys = append(keys, faint.Render("/ search · tab status · R refresh · q quit"))
	out := lipgloss.NewStyle().Width(max(m.w, 20)).Render(strings.Join(keys, faint.Render(" · ")))
	if m.msg != "" {
		out = m.msg + "\n" + out
	}
	return out
}

func (m dashModel) View() string {
	if m.w == 0 {
		return ""
	}
	var body string
	switch {
	case m.wide():
		dw := m.w - m.w*45/100 - 4
		detail := lipgloss.NewStyle().Width(dw).Height(m.h - 6).PaddingLeft(2).BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
			BorderForeground(lipgloss.Color("240")).Render(m.detail(dw - 2))
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), detail)
	case m.details:
		body = lipgloss.NewStyle().Height(m.h - 6).Render(m.detail(m.w - 2))
	default:
		body = m.list.View()
	}
	return m.header() + "\n\n" + body + "\n" + m.footer()
}

// posts: the dashboard in a terminal, a list otherwise (or with --json).
func (e *Env) posts(ctx context.Context, args []string) error {
	fs := e.flags("posts", "posts [--days N] [--status draft|scheduled|published|problems] [--json]")
	days := fs.Int("days", 60, "posts created in the last N days (and anything scheduled)")
	status := fs.String("status", "", "only drafts, scheduled, published or problems")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	me, err := e.me(ctx, c)
	if err != nil {
		return err
	}
	tab := 0
	for i, t := range dashTabs {
		if strings.EqualFold(t.name, *status) || strings.EqualFold(strings.TrimSuffix(t.name, "s"), *status) {
			tab = i
		}
	}
	load := func() ([]api.Post, error) {
		all, err := c.Posts(ctx, time.Now().Add(-time.Duration(*days)*24*time.Hour))
		return all, err
	}
	all, err := load()
	if err != nil {
		return err
	}
	if *asJSON || !e.Interactive {
		var out []api.Post
		for _, p := range all {
			if dashTabs[tab].in(p) {
				out = append(out, p)
			}
		}
		if *asJSON {
			return e.printJSON(out)
		}
		for _, p := range out {
			e.printf("%s  %-42s %s\n", faint.Render(p.ID[:8]), truncate(p.Title(), 42), dashItem{p}.Description())
		}
		return nil
	}
	selectID, msg := "", ""
	for {
		res, err := tea.NewProgram(newDash(all, me, tab, selectID, msg), tea.WithAltScreen()).Run()
		if err != nil {
			return err
		}
		m := res.(dashModel)
		tab, msg = m.tab, ""
		if p := m.selected(); p != nil {
			selectID = p.ID
		}
		switch m.action {
		case "":
			return nil
		case "R":
		default:
			msg = e.dashAction(ctx, c, m.chosen, m.action)
		}
		if all, err = load(); err != nil {
			return err
		}
	}
}

// dashAction runs an action outside the full screen (its questions show normally) and returns its last line.
func (e *Env) dashAction(ctx context.Context, c *api.Client, p *api.Post, key string) string {
	fresh, err := c.Post(ctx, p.ID)
	if err != nil {
		return red.Render("✗ " + err.Error())
	}
	var buf bytes.Buffer
	out := e.Out
	e.Out = io.MultiWriter(&buf)
	if key == "p" {
		err = e.publish(ctx, []string{fresh.ID, "--wait=false"})
	} else {
		err = e.runPostAction(ctx, fresh, key)
	}
	e.Out = out
	switch {
	case errors.Is(err, errSilent):
		return faint.Render("Nothing changed.")
	case err != nil:
		return red.Render("✗ " + err.Error())
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	return lines[0]
}
