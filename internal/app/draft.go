package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/charmbracelet/huh"
)

// mktauto draft --from-git (ADR-081): an app's commits since its last release become a "what's new" post. The AI writes
// it from the changes (or a plain list when the AI isn't set up); then it goes through mktauto post: step by step in a
// terminal (caption filled in), or straight to a draft with flags.

type gitChange struct{ subject, body string }

// internal: commits that don't change anything a user sees.
var internalCommit = regexp.MustCompile(`(?i)^(chore|test|tests|ci|build|docs|refactor|style|deps|wip|merge)(\(.*\))?!?:|^merge |^bump |^release |^v?\d+\.\d+\.\d+$`)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", errors.New(strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// releaseRange: the version being announced (a tag on HEAD, else "") and where its changes start (the tag before it;
// "" = the whole history).
func releaseRange(dir, since string) (version, from string, err error) {
	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return "", "", fmt.Errorf("%s isn't a git repository (--repo DIR)", dir)
	}
	version, _ = git(dir, "describe", "--tags", "--exact-match", "HEAD")
	if since != "" {
		if _, err := git(dir, "rev-parse", "--verify", since+"^{commit}"); err != nil {
			return "", "", fmt.Errorf("--since %s: no such tag or commit", since)
		}
		return version, since, nil
	}
	base := "HEAD"
	if version != "" {
		base = "HEAD^" // HEAD is the release: compare with the tag before it
	}
	from, _ = git(dir, "describe", "--tags", "--abbrev=0", base)
	return version, from, nil
}

func changesSince(dir, from string) ([]gitChange, int, error) {
	rng := "HEAD"
	if from != "" {
		rng = from + "..HEAD"
	}
	out, err := git(dir, "log", "--no-merges", "--format=%s%x1f%b%x1e", "-n", "300", rng)
	if err != nil {
		return nil, 0, err
	}
	var keep []gitChange
	total := 0
	for _, rec := range strings.Split(out, "\x1e") {
		subject, body, _ := strings.Cut(strings.TrimSpace(rec), "\x1f")
		if subject = strings.TrimSpace(subject); subject == "" {
			continue
		}
		total++
		if internalCommit.MatchString(subject) {
			continue
		}
		keep = append(keep, gitChange{subject, strings.TrimSpace(body)})
	}
	return keep, total, nil
}

// changeList: the changes as the AI reads them (≤ 4,500 characters: the helper takes 5,000).
func changeList(app, version, link string, changes []gitChange) string {
	var b strings.Builder
	fmt.Fprintf(&b, "App: %s\n", app)
	if version != "" {
		fmt.Fprintf(&b, "Version: %s\n", version)
	}
	if link != "" {
		fmt.Fprintf(&b, "Link: %s\n", link)
	}
	b.WriteString("Changes:\n")
	for _, c := range changes {
		line := "- " + c.subject
		if c.body != "" {
			first, _, _ := strings.Cut(c.body, "\n")
			if len(first) > 0 && len(first) < 160 {
				line += " (" + strings.TrimSpace(first) + ")"
			}
		}
		if b.Len()+len(line) > 4500 {
			b.WriteString("- …and more\n")
			break
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

var conventional = regexp.MustCompile(`(?i)^(feat|fix|perf|feature|add|added|new)(\(.*\))?!?:\s*`)

// plainPost: the post without AI: the version and a short list of what changed.
func plainPost(app, version, link string, changes []gitChange) string {
	title := "What's new in " + app
	if version != "" {
		title += " " + strings.TrimPrefix(version, "v")
	}
	var b strings.Builder
	b.WriteString(title + ":\n")
	for i, c := range changes {
		if i == 6 {
			fmt.Fprintf(&b, "…and %d more\n", len(changes)-6)
			break
		}
		s := conventional.ReplaceAllString(c.subject, "")
		if s != "" {
			s = strings.ToUpper(s[:1]) + s[1:]
		}
		b.WriteString("• " + s + "\n")
	}
	if link != "" {
		b.WriteString("\n" + link)
	}
	return strings.TrimSpace(b.String())
}

// draftOwnFlags takes the draft command's own flags out of args; the rest go to mktauto post.
func draftOwnFlags(args []string) (own map[string]string, rest []string, err error) {
	own = map[string]string{}
	valued := map[string]bool{"repo": true, "since": true, "app": true, "language": true, "max": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch {
		case !strings.HasPrefix(a, "-"):
			rest = append(rest, a)
		case name == "from-git" || name == "print":
			own[name] = "true"
		case valued[name] && hasValue:
			own[name] = value
		case valued[name]:
			if i+1 >= len(args) {
				return nil, nil, withCode(exitUsage, fmt.Errorf("--%s needs a value", name))
			}
			own[name] = args[i+1]
			i++
		default:
			rest = append(rest, a)
		}
	}
	return own, rest, nil
}

const draftHelp = `Usage: mktauto draft --from-git [--repo DIR] [--since TAG] [--app NAME] [--link URL] [--language L] [--print]
                     [any mktauto post flag: --project, --accounts, --files, --when, --hashtags, --preset…]
An app's changes since its last release (git tags) become a "what's new" post: the AI writes it from the commits
(internal ones such as chore:, test:, docs: are left out), or a plain list when the AI isn't set up.
In a terminal without --accounts: the post wizard with that caption filled in (pick screenshots, accounts, time).
With flags: saved as a draft (or --when …). --print only shows the text.

  --repo DIR        the app's git repository (default: this folder)
  --since TAG       start after this tag or commit (default: the tag before the newest one)
  --app NAME        the app's name (default: the repository folder's name)
  --link URL        a link to put in the post (store page, release notes); also used as the post's link
  --language L      write it in this language (default English)
  --max N           at most N characters (default 500)

Example
  mktauto draft --from-git --repo ~/code/reelwise --app ReelWise --link https://play.google.com/store/apps/details?id=… \
    --project ReelWise --accounts facebook,instagram,x --thread-for x --files screenshot.png
`

func (e *Env) draft(ctx context.Context, args []string) error {
	own, rest, err := draftOwnFlags(args)
	if err != nil {
		return err
	}
	if own["from-git"] == "" {
		fmt.Fprint(e.Err, draftHelp)
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			return nil
		}
		return withCode(exitUsage, errors.New("add --from-git (the only source for now)"))
	}
	repo := firstNonEmpty(own["repo"], e.Dir)
	if abs, err := filepath.Abs(repo); err == nil {
		repo = abs
	}
	version, from, err := releaseRange(repo, own["since"])
	if err != nil {
		return err
	}
	changes, total, err := changesSince(repo, from)
	if err != nil {
		return err
	}
	since := "the first commit"
	if from != "" {
		since = from
	}
	if len(changes) == 0 {
		return fmt.Errorf("no changes users would notice since %s (%s, all internal)", since, plural(total, "commit"))
	}
	app := firstNonEmpty(own["app"], filepath.Base(repo))
	link := ""
	for i := 0; i < len(rest); i++ {
		if n, v, ok := strings.Cut(strings.TrimLeft(rest[i], "-"), "="); n == "link" && strings.HasPrefix(rest[i], "-") {
			if ok {
				link = v
			} else if i+1 < len(rest) {
				link = rest[i+1]
			}
		}
	}
	e.printf("%s %s %s since %s: %s (%s left out as internal)\n", brand.Render(app), firstNonEmpty(version, "(not tagged yet)"), faint.Render("·"), since,
		plural(len(changes), "change"), plural(total-len(changes), "commit"))

	text := ""
	c, err := e.client()
	if err != nil {
		return err
	}
	p := &poster{env: e, c: c, ask: newAsker(), ctx: ctx}
	if p.aiReady(ctx) {
		maxLen := 500
		fmt.Sscanf(own["max"], "%d", &maxLen)
		res, err := p.askAI(ctx, api.AIRequest{Action: "RELEASE", Text: changeList(app, version, link, changes), MaxLength: maxLen, Language: own["language"]})
		switch {
		case err != nil:
			e.printf("%s AI: %v; using a plain list instead\n", yellow.Render("!"), err)
		case e.Interactive && own["print"] == "":
			if text, err = p.pickSuggestion("AI drafts for "+app, res.Suggestions); err != nil && !errors.Is(err, huh.ErrUserAborted) {
				return err
			}
		case len(res.Suggestions) > 0:
			text = res.Suggestions[0]
		}
	}
	if text == "" {
		text = plainPost(app, version, link, changes)
	}
	if own["print"] != "" {
		fmt.Fprintln(e.dataOut(), text)
		return nil
	}
	postArgs := append([]string{"--caption", text}, rest...)
	if version != "" && !hasFlag(rest, "name") {
		postArgs = append(postArgs, "--name", app+" "+version)
	}
	return e.post(ctx, postArgs)
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		n, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if strings.HasPrefix(a, "-") && n == name {
			return true
		}
	}
	return false
}
