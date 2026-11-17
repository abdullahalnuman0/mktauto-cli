package media

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Videos are measured with ffprobe (part of FFmpeg) when it's installed, so length and shape are known before the
// upload (otherwise the server measures them after it). Nothing breaks without it.

var (
	probeOnce sync.Once
	ffprobe   string
)

// CanProbe: ffprobe was found (MKTAUTO_NO_FFPROBE turns it off).
func CanProbe() bool {
	probeOnce.Do(func() {
		if p, err := exec.LookPath("ffprobe"); err == nil && !envSet("MKTAUTO_NO_FFPROBE") {
			ffprobe = p
		}
	})
	return ffprobe != ""
}

// Probe fills a video's width, height (as displayed, rotation applied) and duration.
func Probe(f *File) {
	if !f.IsVideo() || f.DurationMs > 0 || !CanProbe() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries",
		"stream=width,height:stream_side_data=rotation:stream_tags=rotate:format=duration", "-of", "json", f.Path).Output()
	if err != nil {
		return
	}
	var r struct {
		Streams []struct {
			Width    int               `json:"width"`
			Height   int               `json:"height"`
			Tags     map[string]string `json:"tags"`
			SideData []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &r) != nil || len(r.Streams) == 0 {
		return
	}
	s := r.Streams[0]
	w, h := s.Width, s.Height
	rot := 0.0
	if v, err := strconv.ParseFloat(s.Tags["rotate"], 64); err == nil {
		rot = v
	}
	for _, sd := range s.SideData {
		if sd.Rotation != 0 {
			rot = sd.Rotation
		}
	}
	if int(rot)%180 != 0 { // filmed upright on a phone: shown portrait
		w, h = h, w
	}
	f.Width, f.Height = w, h
	if d, err := strconv.ParseFloat(r.Format.Duration, 64); err == nil {
		f.DurationMs = int(d * 1000)
	}
}

// ProbeAll measures the videos among files, a few at a time.
func ProbeAll(files []File) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	for i := range files {
		if !files[i].IsVideo() {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			Probe(&files[i])
		}(i)
	}
	wg.Wait()
}
