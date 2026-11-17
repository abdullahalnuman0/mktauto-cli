package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/auth"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
)

// Tab completion (ADR-078): `mktauto completion SHELL` prints a script that asks `mktauto __complete` for the words,
// which knows the commands and flags and, from the server (cached a few minutes), project and account names,
// templates, hashtag sets and recent posts.

var commandHelp = map[string]string{
	"post": "post files from this folder", "batch": "one post per file or subfolder", "import": "one post per spreadsheet row",
	"posts": "full-screen list of posts", "calendar": "what's planned", "stats": "results", "inbox": "failures, approvals",
	"status": "recent posts or one post", "edit": "change a saved post", "schedule": "schedule or move a post",
	"unschedule": "back to a draft", "publish": "publish a saved post now", "cancel": "stop what isn't published",
	"delete": "delete a draft", "duplicate": "copy a post", "stop-repeat": "stop a repeating post", "retry": "publish failed accounts again",
	"open": "open on the website", "history": "what was posted from here", "projects": "list projects", "accounts": "list accounts",
	"boards": "Pinterest boards", "ai": "AI caption help", "login": "sign in", "logout": "sign out", "whoami": "who am I",
	"doctor": "check this installation", "preset": "saved sets of post flags", "watch": "post new files as they arrive", "best-times": "when your posts did best", "mcp": "connect AI assistants", "update": "install the latest version", "version": "show the version", "completion": "tab completion script",
}

// commandFlags: each command's flags (post, batch and import add mktauto post's; a test keeps them in step).
var commandFlags = map[string][]string{
	"batch":       {"each", "every", "include-posted", "limit"},
	"import":      {"example"},
	"watch":       {"each", "interval", "existing"},
	"best-times":  {"project", "platform", "days", "json"},
	"mcp":         {"allow"},
	"posts":       {"days", "status", "json"},
	"calendar":    {"days", "project", "json"},
	"stats":       {"days", "project", "refresh", "links", "json"},
	"inbox":       {"all", "read-all", "json"},
	"edit":        {"caption", "caption-file", "hashtags", "link", "name"},
	"schedule":    {"at", "next-slot"},
	"publish":     {"yes", "wait"},
	"cancel":      {"yes"},
	"delete":      {"yes"},
	"duplicate":   {"at", "next-slot"},
	"history":     {"all"},
	"projects":    {"json"},
	"accounts":    {"project", "json"},
	"boards":      {"json"},
	"ai":          {"file", "platform", "max", "language", "json"},
	"login":       {"code", "no-browser"},
	"update":      {"version", "check"},
	"status":      {},
	"unschedule":  {},
	"stop-repeat": {},
	"retry":       {},
	"open":        {},
}

// postArgCommands take a POST as their first argument.
var postArgCommands = map[string]bool{"status": true, "edit": true, "schedule": true, "reschedule": true, "unschedule": true, "publish": true,
	"cancel": true, "delete": true, "duplicate": true, "stop-repeat": true, "retry": true, "open": true, "stats": true}

func postFlagNames() []string {
	var f postFlags
	var out []string
	newPostFlags(&f).VisitAll(func(fl *flag.Flag) { out = append(out, fl.Name) })
	return out
}

func flagsFor(cmd string) []string {
	switch cmd {
	case "post":
		return postFlagNames()
	case "batch", "import", "watch":
		return append(postFlagNames(), commandFlags[cmd]...)
	}
	return commandFlags[cmd]
}

type candidate struct{ value, desc string }

// complete returns the candidates for the last word of args (the words after "mktauto").
func (e *Env) complete(ctx context.Context, args []string) []candidate {
	if len(args) == 0 {
		args = []string{""}
	}
	cur := args[len(args)-1]
	if len(args) == 1 {
		var out []candidate
		for c, d := range commandHelp {
			out = append(out, candidate{c, d})
		}
		return filterPrefix(out, cur)
	}
	cmd, prev := args[0], ""
	if len(args) >= 2 {
		prev = args[len(args)-2]
	}
	if strings.HasPrefix(cur, "-") {
		var out []candidate
		for _, f := range flagsFor(cmd) {
			out = append(out, candidate{"--" + f, ""})
		}
		return filterPrefix(out, cur)
	}
	if strings.HasPrefix(prev, "--") {
		if out, ok := e.completeValue(ctx, cmd, strings.TrimPrefix(prev, "--"), cur); ok {
			return out
		}
	}
	positional := len(args) == 2 || (len(args) > 2 && !strings.HasPrefix(prev, "-"))
	switch {
	case cmd == "preset" && len(args) == 2:
		return filterPrefix([]candidate{{"save", ""}, {"list", ""}, {"show", ""}, {"delete", ""}}, cur)
	case cmd == "preset" && len(args) == 3 && (args[1] == "show" || args[1] == "delete"):
		return presetNames(cur)
	case cmd == "completion" && len(args) == 2:
		return filterPrefix([]candidate{{"bash", ""}, {"zsh", ""}, {"fish", ""}, {"powershell", ""}}, cur)
	case cmd == "ai" && len(args) == 2:
		return filterPrefix([]candidate{{"improve", "3 better versions"}, {"shorten", "fit a limit"}, {"professional", "tone"},
			{"casual", "tone"}, {"hashtags", "suggest hashtags"}, {"translate", "into --language"}}, cur)
	case cmd == "import" && len(args) == 2:
		return filesLike(cur, func(n string) bool { return strings.HasSuffix(strings.ToLower(n), ".csv") })
	case cmd == "boards" && len(args) == 2:
		return e.remoteAccounts(ctx, "", cur, "PINTEREST")
	case postArgCommands[cmd] && len(args) == 2 && positional:
		return e.remotePosts(ctx, cur)
	}
	return nil
}

func (e *Env) completeValue(ctx context.Context, cmd, flagName, cur string) ([]candidate, bool) {
	switch flagName {
	case "project":
		return e.remoteProjects(ctx, cur), true
	case "accounts", "ai-off-for", "tiktok-draft-for":
		return e.remoteAccounts(ctx, "list", cur, ""), true
	case "only", "caption-for", "caption-file-for", "title-for", "youtube-privacy-for", "board-for", "pin-link-for",
		"tiktok-privacy-for", "tiktok-allow-for", "tiktok-brand-for":
		return e.remoteAccounts(ctx, "assign", cur, ""), true
	case "template":
		return e.remoteNames(ctx, "templates", cur), true
	case "preset":
		return presetNames(cur), true
	case "hashtag-set":
		return e.remoteNames(ctx, "sets", cur), true
	case "files":
		return filesLike(cur, func(n string) bool { _, ok := media.Types[strings.ToLower(filepath.Ext(n))]; return ok }), true
	case "cover":
		return filesLike(cur, func(n string) bool {
			m := media.Types[strings.ToLower(filepath.Ext(n))]
			return strings.HasPrefix(m, "image/")
		}), true
	case "caption-file", "file":
		return filesLike(cur, func(string) bool { return true }), true
	case "when":
		if cmd == "batch" || cmd == "import" {
			return filterPrefix([]candidate{{"next-slot", "the next free posting times"}, {"draft", ""}}, cur), true
		}
		return filterPrefix([]candidate{{"draft", ""}, {"now", ""}, {"next-slot", "the next free posting time"}}, cur), true
	case "each":
		return filterPrefix([]candidate{{"image", ""}, {"video", ""}, {"file", ""}, {"folder", ""}}, cur), true
	case "duplicates":
		return filterPrefix([]candidate{{"post", ""}, {"new-only", ""}, {"skip", ""}}, cur), true
	case "tiktok-privacy":
		return filterPrefix([]candidate{{"PUBLIC_TO_EVERYONE", ""}, {"MUTUAL_FOLLOW_FRIENDS", ""}, {"FOLLOWER_OF_CREATOR", ""}, {"SELF_ONLY", ""}}, cur), true
	case "youtube-privacy":
		return filterPrefix([]candidate{{"public", ""}, {"unlisted", ""}, {"private", ""}}, cur), true
	case "repeat":
		return filterPrefix([]candidate{{"daily", ""}, {"weekly", ""}, {"biweekly", ""}, {"monthly", ""}}, cur), true
	case "tiktok-brand":
		return filterPrefix([]candidate{{"yours", "your own brand"}, {"partner", "branded content"}, {"yours,partner", ""}}, cur), true
	case "tiktok-allow":
		return filterPrefix([]candidate{{"comments", ""}, {"duet", ""}, {"stitch", ""}, {"comments,duet,stitch", ""}}, cur), true
	case "allow":
		return filterPrefix([]candidate{{"read", ""}, {"drafts", "default"}, {"schedule", ""}, {"publish", ""}}, cur), true
	case "status":
		return filterPrefix([]candidate{{"drafts", ""}, {"scheduled", ""}, {"published", ""}, {"problems", ""}}, cur), true
	case "platform":
		var out []candidate
		for _, p := range []string{"facebook", "instagram", "threads", "x", "pinterest", "tiktok", "youtube"} {
			out = append(out, candidate{p, ""})
		}
		return filterPrefix(out, cur), true
	}
	return nil, false
}

func filterPrefix(list []candidate, cur string) []candidate {
	var out []candidate
	for _, c := range list {
		if strings.HasPrefix(strings.ToLower(c.value), strings.ToLower(cur)) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].value < out[j].value })
	return out
}

// filesLike: files in the folder (comma lists: completes the last name).
func filesLike(cur string, ok func(string) bool) []candidate {
	head, last := "", cur
	if i := strings.LastIndex(cur, ","); i >= 0 {
		head, last = cur[:i+1], cur[i+1:]
	}
	dir := filepath.Dir(last)
	if !strings.ContainsAny(last, `/\`) {
		dir = "."
	}
	entries, _ := os.ReadDir(dir)
	var out []candidate
	for _, en := range entries {
		name := en.Name()
		if dir != "." {
			name = filepath.Join(dir, name)
		}
		if strings.HasPrefix(en.Name(), ".") || !strings.HasPrefix(strings.ToLower(name), strings.ToLower(last)) {
			continue
		}
		if en.IsDir() {
			out = append(out, candidate{head + name + string(os.PathSeparator), ""})
		} else if ok(en.Name()) && !strings.Contains(head, name+",") {
			out = append(out, candidate{head + name, ""})
		}
	}
	return out
}

// ------------------------------------------------------------------ what the server knows (cached)

type completionCache struct {
	Server    string       `json:"server"`
	At        time.Time    `json:"at"`
	Projects  []string     `json:"projects"`
	Accounts  []candidate2 `json:"accounts"`
	Groups    []string     `json:"groups"`
	Templates []string     `json:"templates"`
	Sets      []string     `json:"sets"`
	Posts     []candidate2 `json:"posts"`
}

type candidate2 struct {
	Value    string `json:"value"`
	Desc     string `json:"desc"`
	Platform string `json:"platform,omitempty"`
}

func cachePath() string {
	dir, err := auth.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "completion-cache.json")
}

// remote loads the names from the server at most every 5 minutes (2 s limit; nothing on errors).
func (e *Env) remote(ctx context.Context) *completionCache {
	path := cachePath()
	var cc completionCache
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &cc) == nil && cc.Server == e.Server && time.Since(cc.At) < 5*time.Minute {
		return &cc
	}
	c, err := e.client()
	if err != nil {
		return &completionCache{}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cc = completionCache{Server: e.Server, At: time.Now()}
	if ps, err := c.Projects(ctx); err == nil {
		for _, p := range liveProjects(ps) {
			cc.Projects = append(cc.Projects, p.Name)
		}
	}
	if accs, err := c.Accounts(ctx); err == nil {
		for _, a := range accs {
			name := a.DisplayName
			if a.Handle != "" {
				name = "@" + strings.TrimPrefix(a.Handle, "@")
			}
			cc.Accounts = append(cc.Accounts, candidate2{Value: name, Desc: compose.PlatformLabel[a.Platform], Platform: a.Platform})
		}
	}
	if gs, err := c.AccountGroups(ctx); err == nil {
		for _, g := range gs {
			cc.Groups = append(cc.Groups, g.Name)
		}
	}
	if ts, err := c.Templates(ctx); err == nil {
		for _, t := range ts {
			cc.Templates = append(cc.Templates, t.Name)
		}
	}
	if ss, err := c.HashtagSets(ctx); err == nil {
		for _, s := range ss {
			cc.Sets = append(cc.Sets, s.Name)
		}
	}
	if posts, err := recentPosts(ctx, c, 30); err == nil {
		for i, p := range posts {
			if i == 30 {
				break
			}
			cc.Posts = append(cc.Posts, candidate2{Value: p.ID[:8], Desc: strings.ToLower(p.Status) + " · " + truncate(p.Title(), 40)})
		}
	}
	if data, err := json.Marshal(cc); err == nil && path != "" {
		_ = os.WriteFile(path, data, 0o600)
	}
	return &cc
}

func (e *Env) remoteProjects(ctx context.Context, cur string) []candidate {
	var out []candidate
	for _, p := range e.remote(ctx).Projects {
		out = append(out, candidate{quoteIfSpaced(p), ""})
	}
	return filterPrefix(out, cur)
}

// remoteAccounts: "list" completes the last of a comma list (platform names and group:NAME too); "assign" adds "=".
func (e *Env) remoteAccounts(ctx context.Context, mode, cur, platform string) []candidate {
	cc := e.remote(ctx)
	head, last := "", cur
	if mode == "list" {
		if i := strings.LastIndex(cur, ","); i >= 0 {
			head, last = cur[:i+1], cur[i+1:]
		}
	}
	var out []candidate
	seenPlatform := map[string]bool{}
	for _, a := range cc.Accounts {
		if platform != "" && a.Platform != platform {
			continue
		}
		v := a.Value
		if mode == "assign" {
			v += "="
		}
		out = append(out, candidate{head + quoteIfSpaced(v), a.Desc})
		if mode != "" && a.Platform != "" {
			short := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(a.Platform, "_PAGE"), "_PROFILE"))
			if !seenPlatform[short] {
				seenPlatform[short] = true
				p := short
				if mode == "assign" {
					p += "="
				}
				out = append(out, candidate{head + p, "every " + compose.PlatformLabel[a.Platform] + " account"})
			}
		}
	}
	if mode == "list" {
		for _, g := range cc.Groups {
			out = append(out, candidate{head + quoteIfSpaced("group:"+g), "account group"})
		}
	}
	return filterPrefix(out, head+last)
}

func (e *Env) remoteNames(ctx context.Context, kind, cur string) []candidate {
	cc := e.remote(ctx)
	list := cc.Templates
	if kind == "sets" {
		list = cc.Sets
	}
	var out []candidate
	for _, n := range list {
		out = append(out, candidate{quoteIfSpaced(n), ""})
	}
	return filterPrefix(out, cur)
}

func (e *Env) remotePosts(ctx context.Context, cur string) []candidate {
	out := []candidate{{"last", "the newest post from this folder"}}
	for _, p := range e.remote(ctx).Posts {
		out = append(out, candidate{p.Value, p.Desc})
	}
	return filterPrefix(out, cur)
}

// quoteIfSpaced: names with spaces need quotes on the command line.
func quoteIfSpaced(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// ------------------------------------------------------------------ __complete and the shell scripts

func (e *Env) completeCommand(ctx context.Context, args []string) error {
	shell := "bash"
	if len(args) > 0 && strings.HasPrefix(args[0], "--shell=") {
		shell, args = strings.TrimPrefix(args[0], "--shell="), args[1:]
	}
	for _, c := range e.complete(ctx, args) {
		switch shell {
		case "zsh":
			v := strings.ReplaceAll(c.value, ":", `\:`)
			if c.desc != "" {
				fmt.Fprintf(e.Out, "%s:%s\n", v, c.desc)
			} else {
				fmt.Fprintln(e.Out, v)
			}
		case "fish", "powershell":
			fmt.Fprintf(e.Out, "%s\t%s\n", c.value, c.desc)
		default:
			fmt.Fprintln(e.Out, c.value)
		}
	}
	return nil
}

const bashCompletion = `# mktauto tab completion for bash. Install: echo 'eval "$(mktauto completion bash)"' >> ~/.bashrc
_mktauto() {
  local IFS=$'\n'
  COMP_WORDBREAKS=${COMP_WORDBREAKS//:/}
  COMP_WORDBREAKS=${COMP_WORDBREAKS//=/}
  COMPREPLY=($(mktauto __complete --shell=bash "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null))
  if [[ ${#COMPREPLY[@]} -eq 1 && ( "${COMPREPLY[0]}" == *= || "${COMPREPLY[0]}" == */ ) ]]; then compopt -o nospace 2>/dev/null; fi
}
complete -o default -F _mktauto mktauto
`

const zshCompletion = `#compdef mktauto
# mktauto tab completion for zsh. Install: echo 'eval "$(mktauto completion zsh)"' >> ~/.zshrc (after compinit)
_mktauto() {
  local -a items
  items=("${(@f)$(mktauto __complete --shell=zsh "${(@)words[2,CURRENT]}" 2>/dev/null)}")
  [[ -n "${items[1]}" ]] && _describe -t values 'mktauto' items -S ''
}
compdef _mktauto mktauto
`

const fishCompletion = `# mktauto tab completion for fish. Install: mktauto completion fish > ~/.config/fish/completions/mktauto.fish
complete -c mktauto -f -a '(mktauto __complete --shell=fish (commandline -opc)[2..-1] (commandline -ct) 2>/dev/null)'
`

const powershellCompletion = `# mktauto tab completion for PowerShell. Install: mktauto completion powershell >> $PROFILE
Register-ArgumentCompleter -Native -CommandName mktauto -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.ToString() })
  if ($wordToComplete -eq '') { $words += '' }
  mktauto __complete --shell=powershell @words 2>$null | ForEach-Object {
    $parts = $_ -split "` + "`" + `t", 2
    $desc = $parts[0]
    if ($parts.Count -gt 1 -and $parts[1]) { $desc = $parts[1] }
    [System.Management.Automation.CompletionResult]::new($parts[0], $parts[0], 'ParameterValue', $desc)
  }
}
`

func (e *Env) completion(args []string) error {
	scripts := map[string]string{"bash": bashCompletion, "zsh": zshCompletion, "fish": fishCompletion, "powershell": powershellCompletion}
	if len(args) == 1 {
		if s, ok := scripts[strings.ToLower(args[0])]; ok {
			fmt.Fprint(e.Out, s)
			return nil
		}
	}
	fmt.Fprint(e.Err, `Usage: mktauto completion bash|zsh|fish|powershell
Tab completes commands, flags, your projects, accounts, groups, templates, files and recent posts.
  bash        echo 'eval "$(mktauto completion bash)"' >> ~/.bashrc
  zsh         echo 'eval "$(mktauto completion zsh)"' >> ~/.zshrc
  fish        mktauto completion fish > ~/.config/fish/completions/mktauto.fish
  PowerShell  mktauto completion powershell >> $PROFILE
Then open a new terminal.
`)
	return errors.New("choose a shell")
}

func presetNames(cur string) []candidate {
	all, _ := loadPresets()
	var out []candidate
	for n, flags := range all {
		out = append(out, candidate{n, truncate(strings.Join(flags, " "), 60)})
	}
	return filterPrefix(out, cur)
}
