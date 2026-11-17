// Package auth keeps the tool's settings and its terminal session token.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

const keyringService = "mktauto"

// DefaultServer is the public MKTAuto API (the only built-in value; changeable with --server or MKTAUTO_SERVER).
const DefaultServer = "https://api.mktauto.site"

// Config is stored in the user's config folder (never in the folder being posted from).
type Config struct {
	Server string `json:"server"`
	// LastProject remembers the project chosen per folder (absolute path → project id).
	LastProject map[string]string `json:"lastProject,omitempty"`
	// LastUpdateCheck: when the tool last asked GitHub for a newer version (Phase D).
	LastUpdateCheck string `json:"lastUpdateCheck,omitempty"`
	LatestVersion   string `json:"latestVersion,omitempty"`
}

// Dir is ~/.config/mktauto (Linux), ~/Library/Application Support/mktauto (macOS), %AppData%\mktauto (Windows),
// or MKTAUTO_CONFIG_DIR.
func Dir() (string, error) {
	if d := os.Getenv("MKTAUTO_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "mktauto"), nil
}

var mu sync.Mutex

func LoadConfig() (*Config, error) {
	mu.Lock()
	defer mu.Unlock()
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	c := &Config{}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err == nil {
		_ = json.Unmarshal(data, c)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if c.LastProject == nil {
		c.LastProject = map[string]string{}
	}
	return c, nil
}

func (c *Config) Save() error {
	mu.Lock()
	defer mu.Unlock()
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	return writeFile(filepath.Join(dir, "config.json"), data, 0o600)
}

// ServerURL: flag/env first, then the saved one, then the default.
func (c *Config) ServerURL(override string) string {
	switch {
	case override != "":
		return override
	case os.Getenv("MKTAUTO_SERVER") != "":
		return os.Getenv("MKTAUTO_SERVER")
	case c.Server != "":
		return c.Server
	}
	return DefaultServer
}

// TokenStore keeps one session token per server.
type TokenStore interface {
	Get(server string) (string, error)
	Set(server, token string) error
	Delete(server string) error
	// Where says where tokens are kept, for `mktauto doctor`.
	Where() string
}

// ErrNoToken: not signed in to this server.
var ErrNoToken = errors.New("not signed in")

// NewTokenStore uses the OS keychain, and a private file when there is none (e.g. a Linux server without a keyring),
// or when MKTAUTO_TOKEN_STORE=file.
func NewTokenStore() TokenStore {
	dir, _ := Dir()
	file := &fileStore{path: filepath.Join(dir, "credentials.json")}
	if os.Getenv("MKTAUTO_TOKEN_STORE") == "file" {
		return file
	}
	return &keyringStore{fallback: file}
}

type keyringStore struct{ fallback *fileStore }

func (k *keyringStore) Get(server string) (string, error) {
	t, err := keyring.Get(keyringService, server)
	if err == nil && t != "" {
		return t, nil
	}
	return k.fallback.Get(server)
}

func (k *keyringStore) Set(server, token string) error {
	if err := keyring.Set(keyringService, server, token); err != nil {
		// No usable keychain: keep it in a file only this user can read.
		return k.fallback.Set(server, token)
	}
	_ = k.fallback.Delete(server)
	return nil
}

func (k *keyringStore) Delete(server string) error {
	err := keyring.Delete(keyringService, server)
	ferr := k.fallback.Delete(server)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) && ferr != nil {
		return err
	}
	return nil
}

func (k *keyringStore) Where() string {
	if _, err := keyring.Get(keyringService, "__probe__"); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return "file " + k.fallback.path + " (no system keychain available)"
	}
	return "the system keychain (" + keychainName() + ")"
}

func keychainName() string {
	switch {
	case strings.HasPrefix(osName(), "windows"):
		return "Windows Credential Manager"
	case strings.HasPrefix(osName(), "darwin"):
		return "macOS Keychain"
	}
	return "Secret Service"
}

type fileStore struct{ path string }

func (f *fileStore) read() map[string]string {
	m := map[string]string{}
	if data, err := os.ReadFile(f.path); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func (f *fileStore) Get(server string) (string, error) {
	if t := f.read()[server]; t != "" {
		return t, nil
	}
	return "", ErrNoToken
}

func (f *fileStore) Set(server, token string) error {
	m := f.read()
	m[server] = token
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(m)
	return writeFile(f.path, data, 0o600)
}

func (f *fileStore) Delete(server string) error {
	m := f.read()
	if _, ok := m[server]; !ok {
		return nil
	}
	delete(m, server)
	data, _ := json.Marshal(m)
	return writeFile(f.path, data, 0o600)
}

func (f *fileStore) Where() string { return "file " + f.path }

// writeFile replaces a file atomically with the given permissions.
func writeFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err2 := os.Rename(tmp, path); err2 != nil {
			return fmt.Errorf("can't save %s: %w", path, err2)
		}
	}
	return nil
}
