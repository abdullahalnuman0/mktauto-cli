// Package compose turns the user's choices into the post the server expects. The rules mirror the website
// (web/src/lib/compose.ts, covers.ts, tiktok.ts); the server re-checks everything and stays the authority.
package compose

import "github.com/abdullahalnuman0/mktauto-cli/internal/api"

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
