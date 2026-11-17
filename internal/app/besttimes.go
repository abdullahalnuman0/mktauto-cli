package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
)

// mktauto best-times (ADR-079): when your own published posts did best, by day of the week and time of day. A
// suggestion from your numbers, not a rule: it needs enough posts with results to mean anything.

// engagement weighs interactions the way the server's top-posts list does (AnalyticsService).
func engagement(m map[string]int64) int64 {
	return m["likes"] + 2*m["comments"] + 3*m["shares"] + 2*m["saves"] + m["clicks"]
}

type slotScore struct {
	day, block int // weekday (0 = Monday), 3-hour block (0 = 00–03)
	total      int64
	n          int
}

func (s slotScore) avg() float64 { return float64(s.total) / float64(s.n) }

func blockLabel(b int) string { return fmt.Sprintf("%02d–%02d", b*3, b*3+3) }

var weekdays = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// scoreSlots buckets each account's result by when it was published (local time).
func scoreSlots(results []timedResult) map[[2]int]*slotScore {
	out := map[[2]int]*slotScore{}
	for _, r := range results {
		day := (int(r.at.Weekday()) + 6) % 7
		key := [2]int{day, r.at.Hour() / 3}
		s := out[key]
		if s == nil {
			s = &slotScore{day: key[0], block: key[1]}
			out[key] = s
		}
		s.total += r.score
		s.n++
	}
	return out
}

type timedResult struct {
	at       time.Time
	score    int64
	platform string
}

func (e *Env) bestTimes(ctx context.Context, args []string) error {
	fs := e.flags("best-times", "best-times [--project NAME] [--platform P] [--days N] [--json]")
	project := fs.String("project", "", "only this project's posts")
	platform := fs.String("platform", "", "only this platform (instagram, tiktok…)")
	days := fs.Int("days", 90, "posts published in the last N days")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	if _, err := e.me(ctx, c); err != nil {
		return err
	}
	pid := ""
	if *project != "" {
		ps, err := c.Projects(ctx)
		if err != nil {
			return err
		}
		p, err := findProject(ps, *project)
		if err != nil {
			return err
		}
		pid = p.ID
	}
	from := time.Now().Add(-time.Duration(*days) * 24 * time.Hour)
	var posts []api.Post
	for _, status := range []string{"COMPLETED", "PARTIALLY_FAILED"} {
		list, err := c.PostsWhere(ctx, &from, status)
		if err != nil {
			return err
		}
		for _, p := range list {
			if pid == "" || p.ProjectID == pid {
				posts = append(posts, p)
			}
		}
	}
	sort.Slice(posts, func(i, j int) bool {
		return posts[i].ScheduledAt != nil && posts[j].ScheduledAt != nil && posts[i].ScheduledAt.After(*posts[j].ScheduledAt)
	})
	if len(posts) > 80 {
		posts = posts[:80]
	}
	want := strings.ToUpper(*platform)
	var mu sync.Mutex
	var results []timedResult
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, p := range posts {
		wg.Add(1)
		go func(p api.Post) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := c.PostMetrics(ctx, p.ID, false)
			if err != nil {
				return
			}
			published := map[string]*time.Time{}
			for _, d := range p.Destinations {
				published[d.ID] = d.PublishedAt
			}
			mu.Lock()
			defer mu.Unlock()
			for _, d := range m.Destinations {
				at := published[d.DestinationID]
				if at == nil {
					at = p.ScheduledAt
				}
				if at == nil || !d.Supported || len(d.Metrics) == 0 || (want != "" && !strings.HasPrefix(d.Platform, want)) {
					continue
				}
				results = append(results, timedResult{at.Local(), engagement(d.Metrics), d.Platform})
			}
		}(p)
	}
	wg.Wait()
	slots := scoreSlots(results)
	var list []*slotScore
	for _, s := range slots {
		if s.n >= 2 { // one post is luck, not a pattern
			list = append(list, s)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].avg() > list[j].avg() })
	if *asJSON {
		type row struct {
			Day     string  `json:"day"`
			Hours   string  `json:"hours"`
			Posts   int     `json:"posts"`
			Average float64 `json:"averageEngagement"`
		}
		var out []row
		for _, s := range list {
			out = append(out, row{weekdays[s.day], blockLabel(s.block), s.n, s.avg()})
		}
		return e.printJSON(out)
	}
	if len(results) < 8 || len(list) == 0 {
		return fmt.Errorf("not enough results yet: %s with numbers in the last %d days (8 or more, at least 2 in the same day and time, are needed)",
			plural(len(results), "published account post"), *days)
	}
	label := "all platforms"
	if want != "" {
		label = compose.PlatformLabel[want]
		if label == "" {
			label = *platform
		}
	}
	e.printf("%s %s, from %s (likes + 2×comments + 3×shares + 2×saves + clicks)\n", bold.Render("Best times"), faint.Render(label),
		plural(len(results), "published post"))
	for i, s := range list[:min(3, len(list))] {
		e.printf("  %d. %s %s  %s\n", i+1, bold.Render(weekdays[s.day]), blockLabel(s.block), faint.Render(fmt.Sprintf("%.0f on average · %s", s.avg(), plural(s.n, "post"))))
	}
	// Heat map: rows = days, columns = 3-hour blocks; darker = better on average.
	maxAvg := list[0].avg()
	e.printf("\n       ")
	for b := 0; b < 8; b++ {
		e.printf("%-6s", blockLabel(b)[:2])
	}
	e.printf("\n")
	shades := []string{"·", "░", "▒", "▓", "█"}
	for d := 0; d < 7; d++ {
		e.printf("  %s  ", weekdays[d])
		for b := 0; b < 8; b++ {
			s := slots[[2]int{d, b}]
			cell := faint.Render("·")
			if s != nil && s.n >= 2 {
				cell = brand.Render(strings.Repeat(shades[1+int(3.99*s.avg()/maxAvg)], 2))
			} else if s != nil {
				cell = faint.Render("1")
			}
			e.printf("%-6s", cell)
		}
		e.printf("\n")
	}
	e.printf("%s\n", faint.Render("· no posts · 1 only one post (not counted) · darker = more engagement. Set posting times on the website (Projects)."))
	return nil
}
