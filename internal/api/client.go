package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Problem is the server's application/problem+json error.
type Problem struct {
	Status     int         `json:"status"`
	Code       string      `json:"code"`
	Detail     string      `json:"detail"`
	Validation *Validation `json:"validation"`
}

func (p *Problem) Error() string {
	if p.Detail != "" {
		return p.Detail
	}
	return fmt.Sprintf("server error (HTTP %d)", p.Status)
}

// IsProblem reports whether err is a server problem with this HTTP status (and code, when given).
func IsProblem(err error, status int, code string) bool {
	var p *Problem
	return errors.As(err, &p) && p.Status == status && (code == "" || p.Code == code)
}

// Client calls the MKTAuto API. Token is the terminal session ("mkc_…"); empty for the public sign-in calls.
type Client struct {
	BaseURL   string
	Token     string
	Version   string
	HTTP      *http.Client
	UserAgent string
}

func New(baseURL, token, version string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(NormalizeServer(baseURL), "/"), Token: token, Version: version,
		HTTP: &http.Client{Timeout: 60 * time.Second}, UserAgent: "mktauto/" + version,
	}
}

// NormalizeServer adds https:// when no scheme is given and drops a trailing slash.
func NormalizeServer(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

// CheckServer refuses a server address that would send the sign-in and the session token in clear text: it must be
// https://, except plain http:// to this computer (localhost, 127.0.0.0/8, ::1) for development and tests.
func CheckServer(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("%q isn't a server address: use one like https://api.mktauto.site", s)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if IsLoopback(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("%s uses plain http://, which would send your sign-in and session token unencrypted: use https://%s instead (http:// is only allowed for localhost)", s, u.Host)
	}
	return fmt.Errorf("%q isn't a server address: use https://", s)
}

// IsLoopback: host is this computer (localhost, 127.0.0.0/8 or ::1).
func IsLoopback(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) (int, error) {
	// Checked again here so that no caller can send a token to an insecure address by mistake.
	if err := CheckServer(c.BaseURL); err != nil {
		return 0, err
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("X-Mktauto-Cli", c.Version)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("can't reach %s: %w", c.BaseURL, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return res.StatusCode, err
	}
	if res.StatusCode >= 400 {
		p := &Problem{Status: res.StatusCode}
		_ = json.Unmarshal(data, p)
		p.Status = res.StatusCode
		if p.Detail == "" {
			p.Detail = http.StatusText(res.StatusCode)
		}
		return res.StatusCode, p
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, fmt.Errorf("unexpected answer from the server: %w", err)
		}
	}
	return res.StatusCode, nil
}

// ---- sign-in (public)

func (c *Client) Redeem(ctx context.Context, code string, d Device) (*SessionToken, error) {
	var out SessionToken
	_, err := c.do(ctx, http.MethodPost, "/api/cli/redeem", map[string]any{"code": code, "device": d}, &out)
	return &out, err
}

func (c *Client) StartDeviceAuth(ctx context.Context, d Device) (*DeviceAuthStart, error) {
	var out DeviceAuthStart
	_, err := c.do(ctx, http.MethodPost, "/api/cli/device-auth", map[string]any{"device": d}, &out)
	return &out, err
}

// Poll returns the session once approved, nil while still waiting.
func (c *Client) Poll(ctx context.Context, deviceCode string) (*PollResult, error) {
	var out PollResult
	_, err := c.do(ctx, http.MethodPost, "/api/cli/device-auth/poll", map[string]any{"deviceCode": deviceCode}, &out)
	return &out, err
}

// ---- session

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	_, err := c.do(ctx, http.MethodGet, "/api/cli/me", nil, &out)
	return &out, err
}

func (c *Client) Logout(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/api/cli/logout", nil, nil)
	return err
}

// ---- what to post to

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out []Project
	_, err := c.do(ctx, http.MethodGet, "/api/projects", nil, &out)
	return out, err
}

func (c *Client) ProjectAccounts(ctx context.Context, projectID string) ([]ProjectAccount, error) {
	var out []ProjectAccount
	_, err := c.do(ctx, http.MethodGet, "/api/projects/"+url.PathEscape(projectID)+"/accounts", nil, &out)
	return out, err
}

func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var out []Account
	_, err := c.do(ctx, http.MethodGet, "/api/accounts", nil, &out)
	return out, err
}

func (c *Client) HashtagSets(ctx context.Context) ([]HashtagSet, error) {
	var out []HashtagSet
	_, err := c.do(ctx, http.MethodGet, "/api/hashtag-sets", nil, &out)
	return out, err
}

func (c *Client) TikTokCreatorInfo(ctx context.Context, accountID string) (*TikTokCreatorInfo, error) {
	var out TikTokCreatorInfo
	_, err := c.do(ctx, http.MethodGet, "/api/accounts/"+url.PathEscape(accountID)+"/tiktok-creator-info", nil, &out)
	return &out, err
}

func (c *Client) NextSlot(ctx context.Context, projectID string) (*NextSlot, error) {
	var out NextSlot
	_, err := c.do(ctx, http.MethodGet, "/api/projects/"+url.PathEscape(projectID)+"/next-slot", nil, &out)
	return &out, err
}

// ---- files

func (c *Client) Duplicates(ctx context.Context, sha256s []string) ([]Duplicate, error) {
	var out []Duplicate
	if len(sha256s) == 0 {
		return out, nil
	}
	_, err := c.do(ctx, http.MethodPost, "/api/cli/duplicates", map[string]any{"sha256": sha256s}, &out)
	return out, err
}

func (c *Client) CreateUpload(ctx context.Context, r UploadRequest) (*UploadResponse, error) {
	var out UploadResponse
	_, err := c.do(ctx, http.MethodPost, "/api/media/uploads", r, &out)
	return &out, err
}

func (c *Client) CompleteUpload(ctx context.Context, assetID string, parts []CompletedPart) (*Media, error) {
	var out Media
	var body any
	if parts != nil {
		body = map[string]any{"parts": parts}
	}
	_, err := c.do(ctx, http.MethodPost, "/api/media/"+url.PathEscape(assetID)+"/complete", body, &out)
	return &out, err
}

func (c *Client) AbortUpload(ctx context.Context, assetID string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/media/"+url.PathEscape(assetID)+"/abort", nil, nil)
	return err
}

func (c *Client) Media(ctx context.Context, id string) (*Media, error) {
	var out Media
	_, err := c.do(ctx, http.MethodGet, "/api/media/"+url.PathEscape(id), nil, &out)
	return &out, err
}

// ---- posts

func (c *Client) CreatePost(ctx context.Context, in PostInput) (*Post, error) {
	var out Post
	_, err := c.do(ctx, http.MethodPost, "/api/posts", in, &out)
	return &out, err
}

func (c *Client) Post(ctx context.Context, id string) (*Post, error) {
	var out Post
	_, err := c.do(ctx, http.MethodGet, "/api/posts/"+url.PathEscape(id), nil, &out)
	return &out, err
}

// Posts lists posts created at or after since (all statuses).
func (c *Client) Posts(ctx context.Context, since time.Time) ([]Post, error) {
	var out []Post
	_, err := c.do(ctx, http.MethodGet, "/api/posts?from="+url.QueryEscape(since.UTC().Format(time.RFC3339)), nil, &out)
	return out, err
}

func (c *Client) Validate(ctx context.Context, postID string) (*Validation, error) {
	var out Validation
	_, err := c.do(ctx, http.MethodPost, "/api/posts/"+url.PathEscape(postID)+"/validate", nil, &out)
	return &out, err
}

// Schedule plans the post; nil keeps the post's saved time.
func (c *Client) Schedule(ctx context.Context, postID string, at *Schedule) (*Post, error) {
	var out Post
	var body any
	if at != nil {
		body = at
	}
	_, err := c.do(ctx, http.MethodPost, "/api/posts/"+url.PathEscape(postID)+"/schedule", body, &out)
	return &out, err
}

func (c *Client) PublishNow(ctx context.Context, postID string) (*Post, error) {
	var out Post
	_, err := c.do(ctx, http.MethodPost, "/api/posts/"+url.PathEscape(postID)+"/publish-now", nil, &out)
	return &out, err
}

func (c *Client) RetryDestination(ctx context.Context, destinationID string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/destinations/"+url.PathEscape(destinationID)+"/retry", nil, nil)
	return err
}

// Unschedule takes a scheduled post back to a draft (also withdraws it from approval).
func (c *Client) Unschedule(ctx context.Context, postID string) (*Post, error) {
	var out Post
	_, err := c.do(ctx, http.MethodPost, "/api/posts/"+url.PathEscape(postID)+"/unschedule", nil, &out)
	return &out, err
}

// Cancel stops what hasn't been published yet; published accounts stay published.
func (c *Client) Cancel(ctx context.Context, postID string) (*Post, error) {
	var out Post
	_, err := c.do(ctx, http.MethodPost, "/api/posts/"+url.PathEscape(postID)+"/cancel", nil, &out)
	return &out, err
}

// DeletePost deletes a draft that was never scheduled.
func (c *Client) DeletePost(ctx context.Context, postID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/api/posts/"+url.PathEscape(postID), nil, nil)
	return err
}

// UpdatePost changes the given fields of a draft or a scheduled post that hasn't started publishing.
func (c *Client) UpdatePost(ctx context.Context, postID string, fields map[string]any) (*Post, error) {
	var out Post
	_, err := c.do(ctx, http.MethodPatch, "/api/posts/"+url.PathEscape(postID), fields, &out)
	return &out, err
}

// NextSlots: the project's next free posting times, in order (fewer when 8 weeks don't have that many).
func (c *Client) NextSlots(ctx context.Context, projectID string, count int) ([]NextSlot, error) {
	var out []NextSlot
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/projects/%s/next-slots?count=%d", url.PathEscape(projectID), count), nil, &out)
	return out, err
}

func (c *Client) AIStatus(ctx context.Context) (*AIStatus, error) {
	var out AIStatus
	_, err := c.do(ctx, http.MethodGet, "/api/ai/status", nil, &out)
	return &out, err
}

// AICaption asks the server's caption helper (IMPROVE, SHORTEN, PROFESSIONAL, CASUAL, HASHTAGS, TRANSLATE).
func (c *Client) AICaption(ctx context.Context, r AIRequest) (*AIResult, error) {
	var out AIResult
	_, err := c.do(ctx, http.MethodPost, "/api/ai/caption", r, &out)
	return &out, err
}

func (c *Client) Templates(ctx context.Context) ([]Template, error) {
	var out []Template
	_, err := c.do(ctx, http.MethodGet, "/api/post-templates", nil, &out)
	return out, err
}

func (c *Client) AccountGroups(ctx context.Context) ([]AccountGroup, error) {
	var out []AccountGroup
	_, err := c.do(ctx, http.MethodGet, "/api/account-groups", nil, &out)
	return out, err
}

// PostsWhere lists posts; from filters on the planned time (posts without one are left out), status on the status.
func (c *Client) PostsWhere(ctx context.Context, from *time.Time, status string) ([]Post, error) {
	q := url.Values{}
	if from != nil {
		q.Set("from", from.UTC().Format(time.RFC3339))
	}
	if status != "" {
		q.Set("status", status)
	}
	var out []Post
	_, err := c.do(ctx, http.MethodGet, "/api/posts?"+q.Encode(), nil, &out)
	return out, err
}

// Duplicate copies a post (files, accounts, texts) as a new draft in the same project.
func (c *Client) Duplicate(ctx context.Context, postID string) (*Post, error) {
	var out Post
	_, err := c.do(ctx, http.MethodPost, "/api/posts/"+url.PathEscape(postID)+"/duplicate", nil, &out)
	return &out, err
}

func (c *Client) StopRepeat(ctx context.Context, postID string) (*Post, error) {
	var out Post
	_, err := c.do(ctx, http.MethodPost, "/api/posts/"+url.PathEscape(postID)+"/stop-repeat", nil, &out)
	return &out, err
}

func (c *Client) Analytics(ctx context.Context, days int, projectID string) (*Analytics, error) {
	return c.AnalyticsRange(ctx, fmt.Sprintf("%dd", days), "", projectID)
}

// AnalyticsRange: range = today, yesterday or Nd; tz = the viewer's IANA timezone ("" = the project's). An older
// server ignores range and tz (and answers for 30 days).
func (c *Client) AnalyticsRange(ctx context.Context, rng, tz, projectID string) (*Analytics, error) {
	q := url.Values{"range": {rng}}
	if d, ok := strings.CutSuffix(rng, "d"); ok {
		q.Set("days", d) // older servers
	}
	if tz != "" {
		q.Set("tz", tz)
	}
	if projectID != "" {
		q.Set("projectId", projectID)
	}
	var out Analytics
	_, err := c.do(ctx, http.MethodGet, "/api/analytics?"+q.Encode(), nil, &out)
	return &out, err
}

// PostMetrics: what each account's copy of the post got (refresh = ask the platforms again now).
func (c *Client) PostMetrics(ctx context.Context, postID string, refresh bool) (*PostMetrics, error) {
	var out PostMetrics
	method, path := http.MethodGet, "/api/posts/"+url.PathEscape(postID)+"/metrics"
	if refresh {
		method, path = http.MethodPost, path+"/refresh"
	}
	_, err := c.do(ctx, method, path, nil, &out)
	return &out, err
}

func (c *Client) Links(ctx context.Context) ([]Link, error) {
	var out []Link
	_, err := c.do(ctx, http.MethodGet, "/api/links", nil, &out)
	return out, err
}

func (c *Client) Notifications(ctx context.Context, unread bool, limit int) ([]Notification, error) {
	var out []Notification
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/notifications?unread=%t&limit=%d", unread, limit), nil, &out)
	return out, err
}

func (c *Client) UnreadCount(ctx context.Context) (int, error) {
	var out struct {
		Count int `json:"count"`
	}
	_, err := c.do(ctx, http.MethodGet, "/api/notifications/unread-count", nil, &out)
	return out.Count, err
}

func (c *Client) MarkRead(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodPost, "/api/notifications/"+url.PathEscape(id)+"/read", nil, nil)
	return err
}

func (c *Client) ReadAll(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/api/notifications/read-all", nil, nil)
	return err
}

// AIAltText asks the server's AI to describe one of the user's images (it sees the image; ADR-080).
func (c *Client) AIAltText(ctx context.Context, assetID, language string) (*AltTextResult, error) {
	var out AltTextResult
	body := map[string]string{"assetId": assetID}
	if language != "" {
		body["language"] = language
	}
	_, err := c.do(ctx, http.MethodPost, "/api/ai/alt-text", body, &out)
	return &out, err
}

// UploadReleaseSummary sends a release summary (ADR-090) to the App Ops app with its package name; the server checks
// every field again.
func (c *Client) UploadReleaseSummary(ctx context.Context, summary json.RawMessage) (*BuildUpload, error) {
	var out BuildUpload
	if _, err := c.do(ctx, http.MethodPost, "/api/agent/builds", summary, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Billing: the wallet; nil (no error) from a server without billing.
func (c *Client) Billing(ctx context.Context) (*Billing, error) {
	var out Billing
	if _, err := c.do(ctx, http.MethodGet, "/api/billing", nil, &out); err != nil {
		if IsProblem(err, 404, "") || IsProblem(err, 403, "cli_not_allowed") { // a server without billing
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// Ledger: the last N days (≤ 30) of charges and top-ups, newest first.
func (c *Client) Ledger(ctx context.Context, days int) ([]LedgerLine, error) {
	var out []LedgerLine
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/billing/ledger?days=%d", days), nil, &out)
	return out, err
}

// Estimate: what a saved post will cost (the server's numbers); nil from a server without billing.
func (c *Client) Estimate(ctx context.Context, postID string) (*Estimate, error) {
	var out Estimate
	if _, err := c.do(ctx, http.MethodGet, "/api/posts/"+url.PathEscape(postID)+"/estimate", nil, &out); err != nil {
		if IsProblem(err, 404, "") || IsProblem(err, 403, "cli_not_allowed") {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}
