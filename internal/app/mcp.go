package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
)

// mktauto mcp (ADR-079): a Model Context Protocol server on stdin/stdout, so AI assistants (Claude Desktop, Claude
// Code, other MCP clients) can read and manage posts through this terminal's sign-in. It can only do what the
// terminal session may (the server enforces that), and less by default: --allow chooses how far it goes.

const mcpProtocol = "2025-06-18"

type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
	level       int            // 0 read, 1 drafts, 2 schedule, 3 publish
	run         func(ctx context.Context, a map[string]any) (any, error)
}

var mcpLevels = map[string]int{"read": 0, "drafts": 1, "schedule": 2, "publish": 3}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}
func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func sArg(a map[string]any, k string) string {
	if v, ok := a[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func iArg(a map[string]any, k string, def int) int {
	if v, ok := a[k].(float64); ok {
		return int(v)
	}
	return def
}

func listArg(a map[string]any, k string) []string {
	var out []string
	switch v := a[k].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		out = splitList(v)
	}
	return out
}

// postSummary: what an assistant needs to know about a post.
func postSummary(p api.Post) map[string]any {
	var accs []map[string]any
	for _, d := range p.Destinations {
		m := map[string]any{"platform": compose.PlatformLabel[d.Platform], "account": d.AccountName, "format": d.Format, "status": d.Status}
		if d.PublishedURL != "" {
			m["url"] = d.PublishedURL
		}
		if d.LastErrorMessage != "" && d.Status != "PUBLISHED" {
			m["error"] = d.LastErrorMessage
		}
		accs = append(accs, m)
	}
	out := map[string]any{"id": p.ID, "title": p.Title(), "status": p.Status, "caption": p.BaseCaption, "accounts": accs}
	if p.ScheduledAt != nil {
		out["scheduledAt"] = p.ScheduledAt.Format(time.RFC3339)
		out["localTime"] = p.LocalDateTime
		out["timezone"] = p.Timezone
	}
	if p.RepeatEvery > 0 {
		out["repeatEveryDays"] = p.RepeatEvery
	}
	return out
}

// runCaptured runs a command as if typed, without a terminal, and returns what it printed.
func (e *Env) runCaptured(ctx context.Context, fn func(*Env) error) (string, error) {
	var out, data bytes.Buffer
	sub := *e
	sub.Out, sub.Data, sub.Err, sub.Interactive = &out, &data, &out, false
	err := fn(&sub)
	text := strings.TrimSpace(out.String() + "\n" + data.String())
	return stripANSI(text), err
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (e *Env) mcpTools(c *api.Client) []mcpTool {
	ro := map[string]any{"readOnlyHint": true, "openWorldHint": false}
	write := map[string]any{"readOnlyHint": false, "destructiveHint": false, "openWorldHint": true}
	destructive := map[string]any{"readOnlyHint": false, "destructiveHint": true, "openWorldHint": true}
	postID := func(ctx context.Context, a map[string]any) (string, error) {
		return e.resolvePost(ctx, c, firstNonEmpty(sArg(a, "post"), "last"))
	}
	cmd := func(f func(*Env, context.Context, []string) error, args ...string) func(context.Context) (any, error) {
		return func(ctx context.Context) (any, error) {
			return e.runCaptured(ctx, func(sub *Env) error { return f(sub, ctx, args) })
		}
	}
	return []mcpTool{
		{Name: "list_projects", Title: "List projects", Description: "Projects this terminal may post to, with their timezones.",
			InputSchema: obj(map[string]any{}), Annotations: ro, run: func(ctx context.Context, a map[string]any) (any, error) {
				return c.Projects(ctx)
			}},
		{Name: "list_accounts", Title: "List accounts", Description: "A project's social accounts (platform, name, status, ticked by default), plus account groups, templates and hashtag sets saved on the website.",
			InputSchema: obj(map[string]any{"project": str("project name or id")}, "project"), Annotations: ro,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				ps, err := c.Projects(ctx)
				if err != nil {
					return nil, err
				}
				p, err := findProject(ps, sArg(a, "project"))
				if err != nil {
					return nil, err
				}
				accs, err := c.ProjectAccounts(ctx, p.ID)
				if err != nil {
					return nil, err
				}
				groups, _ := c.AccountGroups(ctx)
				tpls, _ := c.Templates(ctx)
				sets, _ := c.HashtagSets(ctx)
				return map[string]any{"accounts": accs, "groups": groups, "templates": tpls, "hashtagSets": sets}, nil
			}},
		{Name: "list_posts", Title: "List posts", Description: "Recent posts (planned in the last N days or later, and all drafts), newest first. Filter by status: drafts, scheduled, published or problems.",
			InputSchema: obj(map[string]any{"status": str("drafts, scheduled, published or problems (default: all)"), "days": num("default 30"), "limit": num("default 20")}),
			Annotations: ro, run: func(ctx context.Context, a map[string]any) (any, error) {
				posts, err := recentPosts(ctx, c, iArg(a, "days", 30))
				if err != nil {
					return nil, err
				}
				tab := 0
				for i, t := range dashTabs {
					if strings.EqualFold(t.name, sArg(a, "status")) {
						tab = i
					}
				}
				var out []map[string]any
				for _, p := range posts {
					if dashTabs[tab].in(p) && len(out) < iArg(a, "limit", 20) {
						out = append(out, postSummary(p))
					}
				}
				return out, nil
			}},
		{Name: "get_post", Title: "Get a post", Description: "One post with each account's status, link or error. post = id, its first 8 characters, or \"last\".",
			InputSchema: obj(map[string]any{"post": str("post id, id prefix or last")}, "post"), Annotations: ro,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				id, err := postID(ctx, a)
				if err != nil {
					return nil, err
				}
				p, err := c.Post(ctx, id)
				if err != nil {
					return nil, err
				}
				return postSummary(*p), nil
			}},
		{Name: "calendar", Title: "Calendar", Description: "What's planned in the coming days, and the project's free posting times.",
			InputSchema: obj(map[string]any{"project": str("project name (shows its free posting times)"), "days": num("default 14")}), Annotations: ro,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				args := []string{"--days", fmt.Sprint(iArg(a, "days", 14))}
				if p := sArg(a, "project"); p != "" {
					args = append(args, "--project", p)
				}
				return cmd((*Env).calendar, args...)(ctx)
			}},
		{Name: "get_balance", Title: "Credit balance", Description: "The account's prepaid credit: balance, what's available, spending, and the last days' charges (paying is on the website).",
			InputSchema: obj(map[string]any{"days": num("history days, default 7, at most 30")}), Annotations: ro,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				b, err := c.Billing(ctx)
				if err != nil || b == nil || b.Exempt {
					return b, err
				}
				lines, err := c.Ledger(ctx, iArg(a, "days", 7))
				return map[string]any{"billing": b, "ledger": lines}, err
			}},
		{Name: "stats", Title: "Results", Description: "Results (reach, likes, comments…): of one post per account when post is given, else the last N days per platform and the top posts.",
			InputSchema: obj(map[string]any{"post": str("a post (optional)"), "project": str("project (optional)"), "days": num("default 30"),
				"range": str("today, yesterday, 7d, 30d, 90d or 365d (instead of days)")}), Annotations: ro,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				if sArg(a, "post") != "" {
					id, err := postID(ctx, a)
					if err != nil {
						return nil, err
					}
					return c.PostMetrics(ctx, id, false)
				}
				pid := ""
				if name := sArg(a, "project"); name != "" {
					ps, _ := c.Projects(ctx)
					p, err := findProject(ps, name)
					if err != nil {
						return nil, err
					}
					pid = p.ID
				}
				return c.AnalyticsRange(ctx, firstNonEmpty(sArg(a, "range"), fmt.Sprintf("%dd", iArg(a, "days", 30))), "", pid)
			}},
		{Name: "ai_caption", Title: "AI caption help", Description: "The server's caption helper: improve, shorten, professional, casual, hashtags or translate. Uses the user's daily AI limit.",
			InputSchema: obj(map[string]any{"action": str("improve, shorten, professional, casual, hashtags or translate"), "text": str("the caption"),
				"platform": str("instagram, x, tiktok… (optional)"), "max_length": num("shorten: at most this many characters"), "language": str("translate: into")}, "action", "text"),
			Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": true}, level: 1,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				return c.AICaption(ctx, api.AIRequest{Action: strings.ToUpper(sArg(a, "action")), Text: sArg(a, "text"), Platform: strings.ToUpper(sArg(a, "platform")),
					MaxLength: iArg(a, "max_length", 0), Language: sArg(a, "language")})
			}},
		{Name: "create_post", Title: "Create a post",
			Description: "Creates a post the way `mktauto post` does: uploads the files (paths on this computer, in order), checks every account's rules and saves it. when: draft (default), next-slot, \"YYYY-MM-DD HH:MM\" in the project's timezone, or now (each needs --allow schedule / publish). dry_run checks everything and returns the exact text each account would get, without uploading or saving.",
			InputSchema: obj(map[string]any{
				"project": str("project name or id"), "accounts": strList("account names, @handles, platforms (instagram…) or group:NAME"),
				"files": strList("image/video paths, in posting order (none = text only)"), "caption": str("the caption"),
				"captions": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "an account's own caption: {\"instagram\": \"…\"}"},
				"hashtags": strList("hashtags"), "link": str("link"), "first_comment": str("first comment (Facebook Pages, Instagram)"),
				"title": str("YouTube / Pinterest / TikTok photo title"), "name": str("internal name"), "template": str("a saved template"),
				"made_with_ai":    boolean("label it made with AI where the platform supports it"),
				"tiktok_privacy":  str("PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, FOLLOWER_OF_CREATOR or SELF_ONLY (needed for TikTok)"),
				"youtube_privacy": str("public, unlisted or private"), "when": str("draft, next-slot, YYYY-MM-DD HH:MM or now"),
				"repeat": str("daily, weekly, biweekly, monthly or N days (with a time)"), "duplicates": str("files posted before: new-only (default), post or skip"),
				"dry_run": boolean("check and preview only"),
				"alt_texts": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"},
					"description": "an image's alt text, by file path or name: {\"shot.png\": \"…\"}"},
				"alt_ai": boolean("let the AI write alt text for images that have none"),
				"threads": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"description": "X / Threads: a thread, by account: {\"x\": [\"the post\", \"reply 1\", …]} (the first part replaces that account's caption)"},
			}, "project", "accounts"),
			Annotations: write, level: 1,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				when := strings.ToLower(firstNonEmpty(sArg(a, "when"), "draft"))
				need := 1
				switch {
				case when == "now":
					need = 3
				case when != "draft":
					need = 2
				}
				if need > e.mcpLevel && !mcpBool(a, "dry_run") {
					return nil, fmt.Errorf("when=%s needs mktauto mcp --allow %s (it runs with --allow %s)", when,
						map[int]string{2: "schedule", 3: "publish"}[need], levelName(e.mcpLevel))
				}
				args := []string{"--project", sArg(a, "project"), "--accounts", strings.Join(listArg(a, "accounts"), ","), "--when", when, "--yes",
					"--duplicates", firstNonEmpty(sArg(a, "duplicates"), "new-only")}
				if files := listArg(a, "files"); len(files) > 0 {
					args = append(args, "--files", strings.Join(files, ","))
				}
				for k, flagName := range map[string]string{"caption": "caption", "link": "link", "first_comment": "first-comment", "title": "title", "name": "name",
					"template": "template", "tiktok_privacy": "tiktok-privacy", "youtube_privacy": "youtube-privacy", "repeat": "repeat"} {
					if v := sArg(a, k); v != "" {
						args = append(args, "--"+flagName, v)
					}
				}
				if tags := listArg(a, "hashtags"); len(tags) > 0 {
					args = append(args, "--hashtags", strings.Join(tags, " "))
				}
				if caps, ok := a["captions"].(map[string]any); ok {
					for acc, v := range caps {
						if s, ok := v.(string); ok {
							args = append(args, "--caption-for", acc+"="+s)
						}
					}
				}
				if mcpBool(a, "made_with_ai") {
					args = append(args, "--ai")
				}
				if alts, ok := a["alt_texts"].(map[string]any); ok {
					for file, v := range alts {
						if s, ok := v.(string); ok {
							args = append(args, "--alt", file+"="+s)
						}
					}
				}
				if mcpBool(a, "alt_ai") {
					args = append(args, "--alt-ai")
				}
				if threads, ok := a["threads"].(map[string]any); ok {
					for acc, v := range threads {
						if parts := listArg(map[string]any{"p": v}, "p"); len(parts) > 0 {
							args = append(args, "--caption-for", acc+"="+compose.JoinParts(parts))
						}
					}
				}
				if mcpBool(a, "dry_run") {
					args = append(args, "--dry-run")
				} else {
					args = append(args, "--json")
				}
				return e.runCaptured(ctx, func(sub *Env) error { return sub.post(ctx, args) })
			}},
		{Name: "release_post", Title: "Post about a new release",
			Description: "Writes a \"what's new\" post from an app's git commits since its last release tag (internal commits left out; the AI writes it, or a plain list without AI). With print_only it returns the text; otherwise it saves it as a draft for the given project and accounts (with files, if given).",
			InputSchema: obj(map[string]any{"repo": str("path of the app's git repository (default: where mktauto mcp runs)"), "since": str("start after this tag or commit"),
				"app": str("the app's name"), "link": str("store page or release notes link"), "language": str("default English"),
				"project": str("project (needed unless print_only)"), "accounts": strList("accounts"), "files": strList("screenshots/videos, in order"),
				"print_only": boolean("only return the text")}),
			Annotations: write, level: 1, run: func(ctx context.Context, a map[string]any) (any, error) {
				args := []string{"--from-git"}
				for k, flagName := range map[string]string{"repo": "repo", "since": "since", "app": "app", "link": "link", "language": "language", "project": "project"} {
					if v := sArg(a, k); v != "" {
						args = append(args, "--"+flagName, v)
					}
				}
				if mcpBool(a, "print_only") {
					args = append(args, "--print")
				} else {
					if sArg(a, "project") == "" || len(listArg(a, "accounts")) == 0 {
						return nil, errors.New("give project and accounts, or print_only")
					}
					args = append(args, "--accounts", strings.Join(listArg(a, "accounts"), ","), "--when", "draft", "--yes", "--duplicates", "new-only", "--json")
					if files := listArg(a, "files"); len(files) > 0 {
						args = append(args, "--files", strings.Join(files, ","))
					}
				}
				return e.runCaptured(ctx, func(sub *Env) error { return sub.draft(ctx, args) })
			}},
		{Name: "edit_post", Title: "Edit a post", Description: "Changes a draft's (or a scheduled post's, before it starts publishing) caption, hashtags, link or internal name.",
			InputSchema: obj(map[string]any{"post": str("post"), "caption": str("new caption"), "hashtags": strList("new hashtags (replace)"), "link": str("new link"), "name": str("new internal name")}, "post"),
			Annotations: write, level: 1, run: func(ctx context.Context, a map[string]any) (any, error) {
				id, err := postID(ctx, a)
				if err != nil {
					return nil, err
				}
				args := []string{id}
				for _, k := range []string{"caption", "link", "name"} {
					if _, ok := a[k]; ok {
						args = append(args, "--"+k, sArg(a, k))
					}
				}
				if _, ok := a["hashtags"]; ok {
					args = append(args, "--hashtags", firstNonEmpty(strings.Join(listArg(a, "hashtags"), " "), "-"))
				}
				return cmd((*Env).edit, args...)(ctx)
			}},
		{Name: "duplicate_post", Title: "Duplicate a post", Description: "Copies a post (files, accounts, texts) as a new draft.",
			InputSchema: obj(map[string]any{"post": str("post")}, "post"), Annotations: write, level: 1,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				id, err := postID(ctx, a)
				if err != nil {
					return nil, err
				}
				return cmd((*Env).duplicate, id)(ctx)
			}},
		{Name: "schedule_post", Title: "Schedule a post", Description: "Schedules a draft, or moves a scheduled post: at = \"YYYY-MM-DD HH:MM\" in the project's timezone, or next_slot = true.",
			InputSchema: obj(map[string]any{"post": str("post"), "at": str("YYYY-MM-DD HH:MM"), "next_slot": boolean("the next free posting time")}, "post"),
			Annotations: write, level: 2, run: func(ctx context.Context, a map[string]any) (any, error) {
				id, err := postID(ctx, a)
				if err != nil {
					return nil, err
				}
				if mcpBool(a, "next_slot") {
					return cmd((*Env).schedule, id, "--next-slot")(ctx)
				}
				return cmd((*Env).schedule, id, "--at", sArg(a, "at"))(ctx)
			}},
		{Name: "unschedule_post", Title: "Unschedule a post", Description: "Takes a scheduled post back to a draft.",
			InputSchema: obj(map[string]any{"post": str("post")}, "post"), Annotations: write, level: 2,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				id, err := postID(ctx, a)
				if err != nil {
					return nil, err
				}
				return cmd((*Env).unschedule, id)(ctx)
			}},
		{Name: "cancel_post", Title: "Cancel a post", Description: "Stops what isn't published yet (published accounts stay published).",
			InputSchema: obj(map[string]any{"post": str("post")}, "post"), Annotations: destructive, level: 2,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				id, err := postID(ctx, a)
				if err != nil {
					return nil, err
				}
				return cmd((*Env).cancel, id, "--yes")(ctx)
			}},
		{Name: "delete_draft", Title: "Delete a draft", Description: "Deletes a draft that was never scheduled.",
			InputSchema: obj(map[string]any{"post": str("post")}, "post"), Annotations: destructive, level: 2,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				id, err := postID(ctx, a)
				if err != nil {
					return nil, err
				}
				return cmd((*Env).deletePost, id, "--yes")(ctx)
			}},
		{Name: "publish_post", Title: "Publish a post now", Description: "Publishes a saved post now on every account.",
			InputSchema: obj(map[string]any{"post": str("post")}, "post"), Annotations: destructive, level: 3,
			run: func(ctx context.Context, a map[string]any) (any, error) {
				id, err := postID(ctx, a)
				if err != nil {
					return nil, err
				}
				return cmd((*Env).publish, id, "--yes", "--wait=false")(ctx)
			}},
	}
}

func mcpBool(a map[string]any, k string) bool { v, _ := a[k].(bool); return v }

func levelName(l int) string {
	for n, v := range mcpLevels {
		if v == l {
			return n
		}
	}
	return "drafts"
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (e *Env) mcp(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	allow := fs.String("allow", "drafts", "how far assistants may go: read, drafts, schedule or publish")
	fs.Usage = func() {
		fmt.Fprint(e.Err, `Usage: mktauto mcp [--allow read|drafts|schedule|publish]
An MCP server on stdin/stdout for AI assistants, using this terminal's sign-in and permissions.
  read      list projects, accounts, posts, the calendar and results; AI caption help is off
  drafts    also create posts as drafts, edit and duplicate them, AI caption help (default)
  schedule  also schedule, move, unschedule, cancel and delete drafts
  publish   also publish now
Claude Code:    claude mcp add mktauto -- mktauto mcp --allow drafts
Claude Desktop: "mcpServers": {"mktauto": {"command": "mktauto", "args": ["mcp", "--allow", "drafts"]}}
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	level, ok := mcpLevels[strings.ToLower(*allow)]
	if !ok {
		return withCode(exitUsage, fmt.Errorf("--allow %q: read, drafts, schedule or publish", *allow))
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	e.mcpLevel = level
	var tools []mcpTool
	for _, t := range e.mcpTools(c) {
		if t.level <= level {
			tools = append(tools, t)
		}
	}
	return e.serveMCP(ctx, os.Stdin, e.dataOut(), tools)
}

// serveMCP answers JSON-RPC messages, one per line, until the input ends.
func (e *Env) serveMCP(ctx context.Context, in io.Reader, out io.Writer, tools []mcpTool) error {
	var mu sync.Mutex
	send := func(v any) {
		data, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(data, '\n'))
	}
	reply := func(id json.RawMessage, result any) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	fail := func(id json.RawMessage, code int, msg string) {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	}
	byName := map[string]mcpTool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var m rpcMsg
		if err := json.Unmarshal(line, &m); err != nil {
			fail(json.RawMessage("null"), -32700, "parse error")
			continue
		}
		isRequest := len(m.ID) > 0 && string(m.ID) != "null"
		switch m.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(m.Params, &p)
			version := mcpProtocol
			if p.ProtocolVersion != "" && p.ProtocolVersion <= mcpProtocol {
				version = p.ProtocolVersion
			}
			reply(m.ID, map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "mktauto", "title": "MKTAuto", "version": e.Version},
				"instructions": "Post to the user's social accounts through MKTAuto. Start with list_projects and list_accounts. A post is " +
					"named by its id, its first 8 characters or \"last\". Prefer drafts and dry_run unless the user asks to schedule or publish. " +
					"Allowed here: " + levelName(e.mcpLevel) + ".",
			})
		case "ping":
			reply(m.ID, map[string]any{})
		case "tools/list":
			reply(m.ID, map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(m.Params, &p); err != nil {
				fail(m.ID, -32602, "invalid params")
				continue
			}
			t, ok := byName[p.Name]
			if !ok {
				fail(m.ID, -32602, "unknown tool "+p.Name+" (or not allowed: mktauto mcp --allow …)")
				continue
			}
			if p.Arguments == nil {
				p.Arguments = map[string]any{}
			}
			res, err := t.run(ctx, p.Arguments)
			text := ""
			switch v := res.(type) {
			case string:
				text = v
			case nil:
			default:
				data, _ := json.MarshalIndent(v, "", "  ")
				text = string(data)
			}
			if err != nil {
				msg := err.Error()
				if errors.Is(err, errSilent) && text != "" {
					msg = text
				} else if text != "" {
					msg = text + "\n" + msg
				}
				reply(m.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": strings.TrimSpace(msg)}}, "isError": true})
				continue
			}
			reply(m.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": firstNonEmpty(text, "done")}}})
		default:
			if isRequest {
				fail(m.ID, -32601, "method not found: "+m.Method)
			}
		}
	}
	return scanner.Err()
}
