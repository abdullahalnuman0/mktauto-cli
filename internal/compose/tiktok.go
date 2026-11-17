package compose

import "fmt"

// TikTok's posting rules for apps (developers.tiktok.com content-sharing guidelines; web lib/tiktok.ts).
const (
	TikTokSelfOnly       = "SELF_ONLY"
	TikTokMusicUsageURL  = "https://www.tiktok.com/legal/page/global/music-usage-confirmation/en"
	TikTokBrandedPolicyURL = "https://www.tiktok.com/legal/page/global/bc-policy/en"
)

var TikTokPrivacyLabel = map[string]string{
	"PUBLIC_TO_EVERYONE": "Everyone", "MUTUAL_FOLLOW_FRIENDS": "Friends", "FOLLOWER_OF_CREATOR": "Followers", "SELF_ONLY": "Only me",
}

// TikTokChoice: who can view, interactions (off unless the creator turns them on), commercial content, or an inbox draft.
type TikTokChoice struct {
	Privacy        string
	AllowComments  bool
	AllowDuet      bool
	AllowStitch    bool
	BrandDisclose  bool
	BrandOrganic   bool // "Your brand"
	BrandContent   bool // "Branded content" (paid partnership)
	Draft          bool // send to the creator's TikTok inbox instead of posting
}

// Options are the destination options the server expects.
func (c TikTokChoice) Options() map[string]any {
	if c.Draft {
		return map[string]any{"draft": true}
	}
	o := map[string]any{}
	if c.Privacy != "" {
		o["privacy"] = c.Privacy
	}
	if !c.AllowComments {
		o["disable_comment"] = true
	}
	if !c.AllowDuet {
		o["disable_duet"] = true
	}
	if !c.AllowStitch {
		o["disable_stitch"] = true
	}
	if c.BrandDisclose {
		o["brand_disclose"] = true
		if c.BrandOrganic {
			o["brand_organic"] = true
		}
		if c.BrandContent {
			o["brand_content"] = true
		}
	}
	return o
}

// TikTokProblems blocks posting when TikTok's rules aren't met (mirrors the website and the server's TikTokPolicy).
func TikTokProblems(c TikTokChoice, audited bool, options []string, maxVideoSec int, videoMs []int) []string {
	var out []string
	if c.Draft {
		return out
	}
	switch {
	case c.Privacy == "":
		out = append(out, "Choose who can view this TikTok post")
	case !audited && c.Privacy != TikTokSelfOnly:
		out = append(out, `Until the TikTok audit is approved, TikTok posts must be "Only me"`)
	case len(options) > 0 && !contains(options, c.Privacy):
		out = append(out, fmt.Sprintf("%q isn't available for this TikTok account right now", TikTokPrivacyLabel[c.Privacy]))
	}
	if c.BrandDisclose && !c.BrandContent && !c.BrandOrganic {
		out = append(out, `Commercial content is on: choose "Your brand", "Branded content" or both`)
	}
	if c.BrandContent && c.Privacy == TikTokSelfOnly {
		out = append(out, `Branded content can't be private ("Only me")`)
	}
	if maxVideoSec > 0 {
		for _, ms := range videoMs {
			if ms > maxVideoSec*1000 {
				out = append(out, fmt.Sprintf("This TikTok account can post videos up to %d s", maxVideoSec))
				break
			}
		}
	}
	return out
}

// TikTokLabel is what TikTok will show on the post, if anything.
func TikTokLabel(c TikTokChoice) string {
	switch {
	case !c.BrandDisclose:
		return ""
	case c.BrandContent:
		return "Paid partnership"
	case c.BrandOrganic:
		return "Promotional content"
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
