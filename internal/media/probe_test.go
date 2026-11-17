package media

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProbeMeasuresAVideo(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil || !CanProbe() {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	path := filepath.Join(t.TempDir(), "reel.mp4")
	// 2.5 s, 360×640 (portrait).
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=360x640:d=2.5", "-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg couldn't make a test video: %v %s", err, out)
	}
	f, ok := Describe(path)
	if !ok {
		t.Fatal("describe")
	}
	Probe(&f)
	if f.Width != 360 || f.Height != 640 || f.DurationMs < 2400 || f.DurationMs > 2600 {
		t.Errorf("measured %d×%d, %d ms", f.Width, f.Height, f.DurationMs)
	}
	// Not a video: nothing measured, nothing breaks.
	junk := File{Path: filepath.Join(t.TempDir(), "x.mp4"), Kind: "VIDEO"}
	Probe(&junk)
	if junk.DurationMs != 0 {
		t.Error("junk")
	}
}
