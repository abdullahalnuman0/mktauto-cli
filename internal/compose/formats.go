// Package compose turns the user's choices into the post the server expects. The rules mirror the website
// (web/src/lib/compose.ts, covers.ts, tiktok.ts); the server re-checks everything and stays the authority.
package compose

import (
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
)

// PlatformLabel names platforms the way the website does.
var PlatformLabel = map[string]string{
	"FACEBOOK_PAGE": "Facebook Page", "FACEBOOK_GROUP": "Facebook Group", "FACEBOOK_PROFILE": "Facebook profile",
	"INSTAGRAM": "Instagram", "THREADS": "Threads", "X": "X", "PINTEREST": "Pinterest", "TIKTOK": "TikTok", "YOUTUBE": "YouTube",
}

var FormatLabel = map[string]string{
	"TEXT": "Text", "IMAGE": "Image", "CAROUSEL": "Carousel", "VIDEO": "Video", "REEL": "Reel", "STORY": "Story",
	"SHORT": "Short", "PIN": "Pin", "PHOTO_POST": "Photo post",
}

// Kind of a set of files: TEXT, PHOTO, PHOTOS, VIDEO or MIXED (several videos, or photos and videos).
func Kind(files []api.Media) string {
	videos, images := 0, 0
	for _, m := range files {
		switch m.Kind {
		case "VIDEO":
			videos++
		case "IMAGE":
			images++
		}
	}
	switch {
	case videos > 0 && images > 0, videos > 1:
		return "MIXED"
	case videos == 1:
		return "VIDEO"
	case images > 1:
		return "PHOTOS"
	case images == 1:
		return "PHOTO"
	}
	return "TEXT"
}

// vertical: taller than 4:5 (9:16 is the target for Reels, Shorts and TikTok).
func vertical(m api.Media) bool {
	return m.Width > 0 && m.Height > 0 && float64(m.Width)/float64(m.Height) < 0.8
}

// AutoFormat is the format a platform gets for these files ("" when it can't post them), as on the website:
// short vertical videos become Reels / Shorts, others normal videos.
func AutoFormat(platform string, files []api.Media) string {
	kind := Kind(files)
	var video *api.Media
	for i := range files {
		if files[i].Kind == "VIDEO" {
			video = &files[i]
			break
		}
	}
	seconds := -1.0
	isVertical := false
	if video != nil {
		if video.DurationMs > 0 {
			seconds = float64(video.DurationMs) / 1000
		}
		isVertical = vertical(*video)
	}
	switch platform {
	case "FACEBOOK_PAGE":
		switch kind {
		case "TEXT":
			return "TEXT"
		case "PHOTO":
			return "IMAGE"
		case "PHOTOS":
			return "CAROUSEL"
		case "VIDEO":
			if isVertical && seconds >= 0 && seconds <= 90 {
				return "REEL"
			}
			return "VIDEO"
		}
		return ""
	case "FACEBOOK_GROUP", "FACEBOOK_PROFILE", "THREADS":
		switch kind {
		case "TEXT":
			return "TEXT"
		case "PHOTO":
			return "IMAGE"
		case "VIDEO":
			return "VIDEO"
		}
		return "CAROUSEL"
	case "INSTAGRAM":
		switch kind {
		case "TEXT":
			return ""
		case "PHOTO":
			return "IMAGE"
		case "VIDEO":
			return "REEL"
		}
		return "CAROUSEL"
	case "X":
		switch kind {
		case "TEXT":
			return "TEXT"
		case "PHOTO", "PHOTOS":
			return "IMAGE"
		case "VIDEO":
			return "VIDEO"
		}
		return ""
	case "PINTEREST":
		if kind == "TEXT" {
			return ""
		}
		return "PIN"
	case "TIKTOK":
		switch kind {
		case "VIDEO":
			return "VIDEO"
		case "PHOTO", "PHOTOS":
			return "PHOTO_POST"
		}
		return ""
	case "YOUTUBE":
		if kind != "VIDEO" {
			return ""
		}
		if isVertical && seconds >= 0 && seconds <= 180 {
			return "SHORT"
		}
		return "VIDEO"
	}
	return ""
}

// CantPostReason explains an empty AutoFormat.
func CantPostReason(platform string, files []api.Media) string {
	kind := Kind(files)
	label := PlatformLabel[platform]
	switch {
	case kind == "TEXT":
		return label + " needs a photo or video"
	case platform == "YOUTUBE", platform == "TIKTOK" && kind == "MIXED":
		return label + " needs a single video"
	}
	return label + " can't post these files together"
}

// CommentsOn: platforms with the automatic first comment (Facebook Pages; Instagram except stories).
func CommentsOn(platform, format string) bool {
	return platform == "FACEBOOK_PAGE" || (platform == "INSTAGRAM" && format != "STORY")
}

// CoverImageSupported: a custom cover image for a video (web covers.ts).
func CoverImageSupported(platform, format string, hasVideo bool) bool {
	if !hasVideo {
		return false
	}
	switch platform {
	case "YOUTUBE":
		return format == "VIDEO" || format == "SHORT"
	case "INSTAGRAM":
		return format == "REEL"
	case "PINTEREST":
		return format == "PIN"
	case "FACEBOOK_PAGE":
		return format == "VIDEO" || format == "REEL"
	}
	return false
}

// CoverFrameSupported: a frame of the video as its cover.
func CoverFrameSupported(platform, format string, hasVideo bool) bool {
	return hasVideo && ((platform == "INSTAGRAM" && format == "REEL") || (platform == "PINTEREST" && format == "PIN") || (platform == "TIKTOK" && format == "VIDEO"))
}

// AILabelSupported: the platform's API takes a "made with AI" label (ADR-065); never for TikTok inbox drafts.
func AILabelSupported(platform string, tiktokDraft bool) bool {
	switch platform {
	case "TIKTOK":
		return !tiktokDraft
	case "YOUTUBE", "INSTAGRAM", "THREADS", "X", "PINTEREST":
		return true
	}
	return false
}

// ------------------------------------------------------------------ limits (mirror of web SPECS / backend PlatformRules)

type spec struct{ maxFiles, maxText int }

const fbText = 63_206

var specs = map[string]map[string]spec{
	"FACEBOOK_PAGE":    {"TEXT": {0, fbText}, "IMAGE": {1, fbText}, "CAROUSEL": {10, fbText}, "VIDEO": {1, fbText}, "REEL": {1, fbText}},
	"INSTAGRAM":        {"IMAGE": {1, 2200}, "CAROUSEL": {20, 2200}, "REEL": {1, 2200}, "STORY": {1, 0}},
	"THREADS":          {"TEXT": {0, 500}, "IMAGE": {1, 500}, "VIDEO": {1, 500}, "CAROUSEL": {20, 500}},
	"X":                {"TEXT": {0, 280}, "IMAGE": {4, 280}, "VIDEO": {1, 280}},
	"PINTEREST":        {"PIN": {1, 500}},
	"TIKTOK":           {"VIDEO": {1, 2200}, "PHOTO_POST": {35, 4000}},
	"YOUTUBE":          {"VIDEO": {1, 5000}, "SHORT": {1, 5000}},
	"FACEBOOK_GROUP":   {"TEXT": {0, fbText}, "IMAGE": {1, fbText}, "CAROUSEL": {30, fbText}, "VIDEO": {1, fbText}},
	"FACEBOOK_PROFILE": {"TEXT": {0, fbText}, "IMAGE": {1, fbText}, "CAROUSEL": {30, fbText}, "VIDEO": {1, fbText}},
}

// MaxText is the longest text the platform takes in this format (0 = none or unknown).
func MaxText(platform, format string) int { return specs[platform][format].maxText }

// Fit is the format a platform gets for these files, or "" and the reason it can't post them (too many files too).
func Fit(platform string, files []api.Media) (format, reason string) {
	f := AutoFormat(platform, files)
	if f == "" {
		return "", CantPostReason(platform, files)
	}
	if s, ok := specs[platform][f]; ok && len(files) > s.maxFiles {
		return "", fmt.Sprintf("%s takes up to %d files", PlatformLabel[platform], s.maxFiles)
	}
	return f, ""
}

var trackedLink = regexp.MustCompile(`https?://[^\s/]+/l/[A-Za-z0-9]{7}(?:[^A-Za-z0-9-]|$)`)

// TextLength counts characters the way the platform does (web textLength): code points; Threads counts emoji by
// their UTF-8 bytes; tracked short links gain a 2-character tag when published.
func TextLength(platform, text string) int {
	n := 2 * len(trackedLink.FindAllStringIndex(text, -1))
	for _, r := range text {
		if platform == "THREADS" && emojiLike(r) {
			n += utf8.RuneLen(r)
		} else {
			n++
		}
	}
	return n
}

func emojiLike(r rune) bool {
	return (r >= 0x1F000 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) || r == 0x200D || r == 0xFE0F
}
