package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abdullahalnuman0/mktauto-cli/internal/auth"
)

// Saved presets (ADR-079): a named set of `mktauto post` flags kept in this computer's settings folder, usable from any
// folder with --preset NAME (post, batch, import). Flags given on the command line win over the preset's.

func presetsPath() (string, error) {
	dir, err := auth.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "presets.json"), nil
}

func loadPresets() (map[string][]string, error) {
	path, err := presetsPath()
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

func savePresets(p map[string][]string) error {
	path, err := presetsPath()
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// presetName finds --preset NAME / --preset=NAME in args.
func presetName(args []string) string {
	for i, a := range args {
		switch {
		case a == "--preset" || a == "-preset":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "--preset="):
			return strings.TrimPrefix(a, "--preset=")
		case strings.HasPrefix(a, "-preset="):
			return strings.TrimPrefix(a, "-preset=")
		}
	}
	return ""
}

// expandPreset puts the preset's flags before the command line's (later flags win; repeatable ones add up).
func expandPreset(args []string) ([]string, error) {
	name := presetName(args)
	if name == "" {
		return args, nil
	}
	all, err := loadPresets()
	if err != nil {
		return nil, err
	}
	saved, ok := all[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("no preset %q (mktauto preset list)", name)
	}
	return append(append([]string{}, saved...), args...), nil
}

// notSaved: flags that describe one run, not a preset.
var notSaved = map[string]bool{"preset": true, "dry-run": true, "json": true, "yes": true, "wait": true, "files": true, "caption": true,
	"caption-file": true, "name": true, "title": true, "example": true}

func (e *Env) preset(args []string) error {
	usage := func() error {
		fmt.Fprint(e.Err, `Usage: mktauto preset save NAME [mktauto post flags]   save them (e.g. --project, --accounts, --hashtags, --when, --tiktok-privacy)
       mktauto preset list | show NAME | delete NAME
Then: mktauto post --preset NAME (in a terminal without --files: step by step, with the preset's answers filled in),
      mktauto batch --preset NAME, mktauto import FILE.csv --preset NAME. Flags you add win over the preset's.
Not saved (they belong to one post): --files, --caption, --caption-file, --title, --name, --dry-run, --yes, --json.
`)
		return flag.ErrHelp
	}
	if len(args) == 0 {
		return usage()
	}
	all, err := loadPresets()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(all) == 0 {
			e.printf("No presets yet: mktauto preset save NAME --project … --accounts …\n")
			return nil
		}
		var names []string
		for n := range all {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			e.printf("%-16s %s\n", bold.Render(n), faint.Render(strings.Join(all[n], " ")))
		}
		return nil
	case "show", "delete":
		if len(args) != 2 {
			return usage()
		}
		name := strings.ToLower(args[1])
		saved, ok := all[name]
		if !ok {
			return fmt.Errorf("no preset %q", args[1])
		}
		if args[0] == "show" {
			e.printf("%s\n", strings.Join(saved, " "))
			return nil
		}
		delete(all, name)
		if err := savePresets(all); err != nil {
			return err
		}
		e.printf("%s Deleted %s\n", green.Render("✓"), name)
		return nil
	case "save":
		if len(args) < 3 {
			return usage()
		}
		name := strings.ToLower(strings.TrimSpace(args[1]))
		if name == "" || strings.ContainsAny(name, " =/\\") {
			return errors.New("a preset name is one word")
		}
		var f postFlags
		fs := newPostFlags(&f)
		fs.SetOutput(e.Err)
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return fmt.Errorf("unexpected %q: presets hold flags only", fs.Arg(0))
		}
		var saved, skipped []string
		fs.Visit(func(fl *flag.Flag) {
			if notSaved[fl.Name] {
				skipped = append(skipped, "--"+fl.Name)
				return
			}
			if m, ok := fl.Value.(*multiFlag); ok {
				for _, v := range *m {
					saved = append(saved, "--"+fl.Name+"="+v)
				}
				return
			}
			saved = append(saved, "--"+fl.Name+"="+fl.Value.String())
		})
		if len(saved) == 0 {
			return errors.New("nothing to save: give flags such as --project, --accounts, --hashtags")
		}
		all[name] = saved
		if err := savePresets(all); err != nil {
			return err
		}
		e.printf("%s Saved %s: %s\n", green.Render("✓"), bold.Render(name), faint.Render(strings.Join(saved, " ")))
		if len(skipped) > 0 {
			e.printf("%s\n", faint.Render("Not saved (they belong to one post): "+strings.Join(skipped, " ")))
		}
		e.printf("Use it: mktauto post --preset %s\n", name)
		return nil
	}
	return usage()
}
