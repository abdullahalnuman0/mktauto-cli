package media

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
}

func TestScanDescribesFingerprintsAndReadsCaptions(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "img10.png"), 40, 50)
	writePNG(t, filepath.Join(dir, "img2.png"), 30, 30)
	_ = os.WriteFile(filepath.Join(dir, "reel.MP4"), []byte("not really a video"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notes.pdf"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, ".hidden.png"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "empty.jpg"), nil, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "reel.txt"), []byte("  Reel caption \n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "caption.txt"), []byte("Folder caption"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "mktauto.yml"), []byte("project: Memoshot\naccounts: [instagram, tiktok]\nhashtags: [android]\nmadeWithAI: true\n"), 0o600)

	files, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "img2.png,img10.png,reel.MP4" {
		t.Fatalf("natural order, postable only: %v", names)
	}
	if files[1].Width != 40 || files[1].Height != 50 || files[2].Kind != "VIDEO" || files[2].Mime != "video/mp4" {
		t.Errorf("describe: %+v %+v", files[1], files[2])
	}
	if err := Fingerprint(files, nil); err != nil || len(files[2].SHA256) != 64 || files[0].SHA256 == files[1].SHA256 {
		t.Errorf("fingerprints: %v %+v", err, files)
	}
	if SidecarCaption(files[2]) != "Reel caption" || SidecarCaption(files[0]) != "Folder caption" {
		t.Error("captions: own .txt first, then caption.txt")
	}
	d, err := LoadDefaults(dir)
	if err != nil || d.Project != "Memoshot" || len(d.Accounts) != 2 || d.MadeWithAI == nil || !*d.MadeWithAI {
		t.Errorf("mktauto.yml: %+v %v", d, err)
	}
}
