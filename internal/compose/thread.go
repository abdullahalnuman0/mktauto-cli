package compose

import (
	"strings"
	"unicode"
)

// ThreadMaxParts: the most extra parts after the main post (server PlatformRules.THREAD_MAX_PARTS).
const ThreadMaxParts = 24

// ThreadPlatform: platforms where a post can be a thread of replies (ADR-080).
func ThreadPlatform(platform string) bool { return platform == "X" || platform == "THREADS" }

// SplitThread cuts text into parts that each fit limit (counted the platform's way), at paragraph breaks first, then
// sentence ends, then spaces; a word longer than the limit is cut. firstReserve is room kept free in the first part
// (the hashtags added to the main post). Empty text gives no parts.
func SplitThread(platform, text string, limit, firstReserve int) []string {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return nil
	}
	fits := func(s string, first bool) bool {
		room := limit
		if first {
			room -= firstReserve
		}
		return TextLength(platform, s) <= max(room, 1)
	}
	var parts []string
	cur := ""
	add := func(piece, sep string) {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			return
		}
		if cur == "" {
			cur = piece
		} else if candidate := cur + sep + piece; fits(candidate, len(parts) == 0) {
			cur = candidate
		} else {
			parts = append(parts, cur)
			cur = piece
		}
		// A piece too long by itself: cut it at spaces, then hard.
		for !fits(cur, len(parts) == 0) {
			room := limit
			if len(parts) == 0 {
				room -= firstReserve
			}
			head, tail := cutAt(platform, cur, max(room, 1))
			parts = append(parts, head)
			cur = tail
		}
	}
	for _, para := range strings.Split(text, "\n\n") {
		if fits(para, len(parts) == 0 && cur == "") && cur == "" {
			add(para, "\n\n")
			continue
		}
		sentences := splitSentences(para)
		for i, s := range sentences {
			sep := " "
			if i == 0 {
				sep = "\n\n"
			}
			add(s, sep)
		}
	}
	if strings.TrimSpace(cur) != "" {
		parts = append(parts, cur)
	}
	return parts
}

// splitSentences keeps the ending punctuation with its sentence.
func splitSentences(s string) []string {
	var out []string
	start := 0
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if (r[i] == '.' || r[i] == '!' || r[i] == '?' || r[i] == '\n' || r[i] == '।') && (i+1 == len(r) || unicode.IsSpace(r[i+1])) {
			out = append(out, string(r[start:i+1]))
			start = i + 1
		}
	}
	if start < len(r) {
		out = append(out, string(r[start:]))
	}
	return out
}

// cutAt splits s so the head fits room: at the last space that fits, else hard at room characters.
func cutAt(platform, s string, room int) (string, string) {
	r := []rune(s)
	space, hard := -1, 0
	for i := 1; i <= len(r) && TextLength(platform, string(r[:i])) <= room; i++ {
		hard = i
		if i < len(r) && unicode.IsSpace(r[i]) {
			space = i
		}
	}
	cut := space
	if cut <= 0 {
		cut = max(hard, 1)
	}
	return strings.TrimSpace(string(r[:cut])), strings.TrimSpace(string(r[cut:]))
}

// JoinParts / SplitParts: a thread edited in one text box, parts separated by a line with only "---".
func JoinParts(parts []string) string { return strings.Join(parts, "\n---\n") }

func SplitParts(text string) []string {
	var out []string
	var cur []string
	flush := func() {
		if p := strings.TrimSpace(strings.Join(cur, "\n")); p != "" {
			out = append(out, p)
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "---" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}
