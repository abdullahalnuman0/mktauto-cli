package app

import (
	"context"
	"slices"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
)

// recentPosts: posts planned in the last `days` days or later, plus every draft (the server's "from" filters on the
// planned time, so drafts without one need their own query). Newest first: by planned time, else by creation.
func recentPosts(ctx context.Context, c *api.Client, days int) ([]api.Post, error) {
	from := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	planned, err := c.PostsWhere(ctx, &from, "")
	if err != nil {
		return nil, err
	}
	drafts, err := c.PostsWhere(ctx, nil, "DRAFT")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []api.Post
	for _, p := range append(planned, drafts...) {
		if !seen[p.ID] {
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	key := func(p api.Post) time.Time {
		if p.ScheduledAt != nil {
			return *p.ScheduledAt
		}
		return p.CreatedAt
	}
	slices.SortStableFunc(out, func(a, b api.Post) int { return key(b).Compare(key(a)) })
	return out, nil
}
