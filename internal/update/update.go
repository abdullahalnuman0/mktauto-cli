// Package update finds newer releases on GitHub and replaces the running program with a checked download.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo publishes the releases (ADR-069).
const Repo = "abdullahalnuman0/mktauto-cli"

// Source is where releases are read from (tests point it at a fake server).
type Source struct {
	Web      string // https://github.com/<repo>
	API      string // https://api.github.com/repos/<repo>
	Download string // https://github.com/<repo>/releases/download
	HTTP     *http.Client
}

func GitHub() *Source {
	return &Source{Web: "https://github.com/" + Repo, API: "https://api.github.com/repos/" + Repo, Download: "https://github.com/" + Repo + "/releases/download",
		HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// Latest is the newest non-pre-release version tag, e.g. "v1.2.0". It reads where github.com/<repo>/releases/latest
// redirects to (no rate limit), and asks GitHub's API (60 requests an hour per IP without a token) only if that fails.
func (s *Source) Latest(ctx context.Context) (string, error) {
	if s.Web != "" {
		if tag, err := s.latestFromRedirect(ctx); err == nil {
			return tag, nil
		}
	}
	return s.latestFromAPI(ctx)
}

func (s *Source) latestFromRedirect(ctx context.Context) (string, error) {
	client := *s.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, s.Web+"/releases/latest", nil)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	_, tag, ok := strings.Cut(loc, "/releases/tag/")
	if res.StatusCode/100 != 3 || !ok || tag == "" || strings.Contains(tag, "/") {
		return "", fmt.Errorf("no latest release (HTTP %d)", res.StatusCode)
	}
	return tag, nil
}

func (s *Source) latestFromAPI(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.API+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return "", errors.New("no release is published yet")
	}
	if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests {
		return "", errors.New("GitHub's limit for this network was reached: try again in an hour")
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered HTTP %d", res.StatusCode)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil || r.TagName == "" {
		return "", errors.New("unexpected answer from GitHub")
	}
	return r.TagName, nil
}

// AssetName: mktauto_<version>_<os>_<arch>.tar.gz (.zip on Windows), as the release workflow builds them.
func AssetName(tag, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("mktauto_%s_%s_%s.%s", strings.TrimPrefix(tag, "v"), goos, goarch, ext)
}

// Newer: a is a later version than b ("v1.10.0" > "1.9.3"; a pre-release is older than its release).
func Newer(a, b string) bool {
	pa, preA := parse(a)
	pb, preB := parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return preA == "" && preB != ""
}

func parse(v string) ([3]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	main, pre, _ := strings.Cut(v, "-")
	var out [3]int
	for i, p := range strings.SplitN(main, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out, pre
}

// Fetch downloads this system's release file for tag, checks it against the release's checksums.txt and returns the
// program inside it.
func (s *Source) Fetch(ctx context.Context, tag, goos, goarch string) ([]byte, error) {
	name := AssetName(tag, goos, goarch)
	sums, err := s.get(ctx, tag, "checksums.txt", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("checksums: %w", err)
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return nil, fmt.Errorf("%s isn't in this release (no build for %s/%s?)", name, goos, goarch)
	}
	archive, err := s.get(ctx, tag, name, 200<<20)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s doesn't match its SHA-256 checksum: not installed", name)
	}
	return Extract(archive, goos)
}

func (s *Source) get(ctx context.Context, tag, file string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.Download+"/"+tag+"/"+file, nil)
	res, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", file, res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is unexpectedly large", file)
	}
	return data, err
}

// Extract returns the mktauto program from a release archive.
func Extract(archive []byte, goos string) ([]byte, error) {
	want := "mktauto"
	if goos == "windows" {
		want = "mktauto.exe"
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want {
				r, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer r.Close()
				return io.ReadAll(io.LimitReader(r, 200<<20))
			}
		}
		return nil, errors.New("the download has no mktauto.exe")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the download has no mktauto program")
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == want {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// Replace swaps the program at path for the new one. The running file can't be overwritten on Windows, so it is
// renamed first (and removed on the next run by Cleanup).
func Replace(path string, program []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".mktauto-new-*")
	if err != nil {
		return fmt.Errorf("can't write next to %s (install it somewhere you can write, or run with the needed rights): %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(program); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := path + ".old"
		_ = os.Remove(old)
		if err := os.Rename(path, old); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			_ = os.Rename(old, path)
			return err
		}
		return nil
	}
	return os.Rename(tmp.Name(), path)
}

// Cleanup removes the previous program a Windows update left behind.
func Cleanup(path string) { _ = os.Remove(path + ".old") }
