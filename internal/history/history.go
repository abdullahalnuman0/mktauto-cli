// Package history remembers what was posted from each folder (kept in the tool's config folder, not in yours).
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/auth"
)

type File struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type Entry struct {
	At       time.Time `json:"at"`
	Folder   string    `json:"folder"`
	PostID   string    `json:"postId"`
	Project  string    `json:"project"`
	Action   string    `json:"action"` // draft, scheduled, publishing, approval
	When     string    `json:"when,omitempty"`
	Accounts []string  `json:"accounts"`
	Files    []File    `json:"files"`
	Server   string    `json:"server"`
}

var mu sync.Mutex

func path() (string, error) {
	dir, err := auth.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.json"), nil
}

func load() ([]Entry, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	var out []Entry
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(data, &out)
	return out, nil
}

// Add records a post (keeps the newest 2000 entries).
func Add(e Entry) error {
	mu.Lock()
	defer mu.Unlock()
	all, err := load()
	if err != nil {
		return err
	}
	all = append(all, e)
	if len(all) > 2000 {
		all = all[len(all)-2000:]
	}
	p, _ := path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(all, "", " ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// For lists a folder's posts (all folders when folder is ""), newest first.
func For(folder string) ([]Entry, error) {
	mu.Lock()
	defer mu.Unlock()
	all, err := load()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range all {
		if folder == "" || e.Folder == folder {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}
