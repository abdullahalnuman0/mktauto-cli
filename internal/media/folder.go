package media

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults is the optional mktauto.yml in a folder: what to preselect in the wizard (or use without asking).
type Defaults struct {
	Project     string   `yaml:"project"`  // id or name
	Accounts    []string `yaml:"accounts"` // ids, names, @handles or platforms (instagram, tiktok…)
	Hashtags    []string `yaml:"hashtags"`
	Link        string   `yaml:"link"`
	Caption     string   `yaml:"caption"`
	FirstComment string  `yaml:"firstComment"`
	MadeWithAI  *bool    `yaml:"madeWithAI"`
	When        string   `yaml:"when"` // draft, now, next-slot or "YYYY-MM-DD HH:MM"
}

// LoadDefaults reads mktauto.yml (or .yaml) from dir; a missing file is not an error.
func LoadDefaults(dir string) (*Defaults, error) {
	for _, name := range []string{"mktauto.yml", "mktauto.yaml"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		d := &Defaults{}
		if err := yaml.Unmarshal(data, d); err != nil {
			return nil, err
		}
		return d, nil
	}
	return &Defaults{}, nil
}

// SidecarCaption: "reel.mp4" → text of "reel.txt" next to it, else "caption.txt" in the folder; "" when neither exists.
func SidecarCaption(file File) string {
	dir := filepath.Dir(file.Path)
	base := strings.TrimSuffix(file.Name, filepath.Ext(file.Name))
	for _, name := range []string{base + ".txt", "caption.txt"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}
