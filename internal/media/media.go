// Package media finds the images and videos in a folder and describes them.
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/gif"  // image sizes
	_ "image/jpeg" // image sizes
	_ "image/png"  // image sizes
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	_ "golang.org/x/image/webp" // image sizes
)

// Types MKTAuto accepts (backend MediaPolicy): extension → MIME type.
var Types = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp", ".gif": "image/gif",
	".mp4": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
}

// File is one postable file in the folder.
type File struct {
	Path       string // absolute
	Name       string // file name
	Mime       string
	Kind       string // IMAGE or VIDEO
	Size       int64
	Width      int // images; videos when ffprobe measured them (else the server measures after upload)
	Height     int
	DurationMs int    // videos measured by ffprobe
	SHA256     string // hex, filled by Fingerprint
}

func (f File) IsVideo() bool { return f.Kind == "VIDEO" }

// Scan lists the postable files directly in dir (not subfolders), sorted by name.
func Scan(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		f, ok := Describe(filepath.Join(dir, e.Name()))
		if ok {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i].Name, out[j].Name) })
	return out, nil
}

// Describe returns the file's type, size and (images) dimensions; false when it isn't a postable type.
func Describe(path string) (File, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return File{}, false
	}
	mime, ok := Types[strings.ToLower(filepath.Ext(abs))]
	if !ok {
		return File{}, false
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return File{}, false
	}
	f := File{Path: abs, Name: filepath.Base(abs), Mime: mime, Size: st.Size(), Kind: "IMAGE"}
	if strings.HasPrefix(mime, "video/") {
		f.Kind = "VIDEO"
	} else if r, err := os.Open(abs); err == nil {
		if cfg, _, err := image.DecodeConfig(r); err == nil {
			f.Width, f.Height = cfg.Width, cfg.Height
		}
		r.Close()
	}
	return f, true
}

// Fingerprint fills SHA256 for every file, a few at a time; progress gets the number done.
func Fingerprint(files []File, progress func(done int)) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	done := 0
	sem := make(chan struct{}, max(2, runtime.NumCPU()/2))
	for i := range files {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			h, err := SHA256(files[i].Path)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			files[i].SHA256 = h
			done++
			if progress != nil {
				progress(done)
			}
		}(i)
	}
	wg.Wait()
	return firstErr
}

func SHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// naturalLess sorts "img2" before "img10".
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da != "" && db != "" {
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

func envSet(name string) bool { return os.Getenv(name) != "" }
