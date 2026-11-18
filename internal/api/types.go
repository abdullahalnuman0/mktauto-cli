// Package api talks to the MKTAuto server (the same API the website uses), with a terminal session token.
package api

import "time"

// Device is what the tool reports about this computer at sign-in (shown on the website only).
type Device struct {
	OS         string `json:"os"`
	OSVersion  string `json:"osVersion,omitempty"`
	Arch       string `json:"arch"`
	Hostname   string `json:"hostname,omitempty"`
	CLIVersion string `json:"cliVersion"`
}

type Session struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Permission     string     `json:"permission"` // DRAFT, SCHEDULE, PUBLISH
	ProjectIDs     []string   `json:"projectIds"`
	DailyPostLimit *int       `json:"dailyPostLimit"`
	CreatedVia     string     `json:"createdVia"`
	Hostname       string     `json:"hostname"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	LastUsedAt     *time.Time `json:"lastUsedAt"`
	PostsCreated   int        `json:"postsCreated"`
	FilesUploaded  int        `json:"filesUploaded"`
	BytesUploaded  int64      `json:"bytesUploaded"`
}

// SessionToken is returned once, when a session is created.
type SessionToken struct {
	Token   string  `json:"token"`
	Session Session `json:"session"`
}

type DeviceAuthStart struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURL         string `json:"verificationUrl"`
	VerificationURLComplete string `json:"verificationUrlComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

type PollResult struct {
	Status   string        `json:"status"`
	Interval int           `json:"interval"`
	Session  *SessionToken `json:"session"`
}

type Me struct {
	DisplayName      string  `json:"displayName"`
	Email            string  `json:"email"`
	RequiresApproval bool    `json:"requiresApproval"`
	Session          Session `json:"session"`
	PostsLast24h     int     `json:"postsLast24h"`
	MinCLIVersion    string  `json:"minCliVersion"`
	WebURL           string  `json:"webUrl"`
}

func (m Me) Allows(permission string) bool {
	order := map[string]int{"DRAFT": 0, "SCHEDULE": 1, "PUBLISH": 2}
	return order[m.Session.Permission] >= order[permission]
}

type Project struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Archived bool   `json:"archived"`
}

// ProjectAccount is an account linked to a project.
type ProjectAccount struct {
	AccountID       string `json:"accountId"`
	Platform        string `json:"platform"`
	DisplayName     string `json:"displayName"`
	Handle          string `json:"handle"`
	PublishMode     string `json:"publishMode"`
	Status          string `json:"status"`
	Label           string `json:"label"`
	DefaultSelected bool   `json:"defaultSelected"`
	SortOrder       int    `json:"sortOrder"`
}

func (a ProjectAccount) Usable() bool { return a.Status != "DISABLED" && a.Status != "REAUTH_REQUIRED" }

// Account carries per-account settings (Pinterest boards).
type Account struct {
	ID           string         `json:"id"`
	Platform     string         `json:"platform"`
	DisplayName  string         `json:"displayName"`
	Handle       string         `json:"handle"`
	PublishMode  string         `json:"publishMode"`
	Status       string         `json:"status"`
	StatusReason string         `json:"statusReason"`
	Settings     map[string]any `json:"settings"`
}

type Board struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Boards lists a Pinterest account's boards and its default board id.
func (a Account) Boards() ([]Board, string) {
	var out []Board
	if raw, ok := a.Settings["boards"].([]any); ok {
		for _, b := range raw {
			if m, ok := b.(map[string]any); ok {
				id, _ := m["id"].(string)
				name, _ := m["name"].(string)
				if id != "" {
					out = append(out, Board{ID: id, Name: name})
				}
			}
		}
	}
	def, _ := a.Settings["default_board_id"].(string)
	return out, def
}

type HashtagSet struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type UploadRequest struct {
	ProjectID string `json:"projectId,omitempty"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256,omitempty"`
}

type UploadPart struct {
	PartNumber int    `json:"partNumber"`
	URL        string `json:"url"`
}

type UploadResponse struct {
	AssetID   string            `json:"assetId"`
	Mode      string            `json:"mode"` // SINGLE or MULTIPART
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	PartSize  int64             `json:"partSize"`
	Parts     []UploadPart      `json:"parts"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type CompletedPart struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
}

type Media struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"` // IMAGE, VIDEO
	MimeType         string `json:"mimeType"`
	OriginalFilename string `json:"originalFilename"`
	SizeBytes        int64  `json:"sizeBytes"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	DurationMs       int    `json:"durationMs"`
	StorageStatus    string `json:"storageStatus"`
}

// Use is one post destination that already used a file.
type Use struct {
	PostID       string     `json:"postId"`
	PostTitle    string     `json:"postTitle"`
	PostStatus   string     `json:"postStatus"`
	AccountID    string     `json:"accountId"`
	AccountName  string     `json:"accountName"`
	Platform     string     `json:"platform"`
	Status       string     `json:"status"`
	ScheduledAt  *time.Time `json:"scheduledAt"`
	PublishedAt  *time.Time `json:"publishedAt"`
	PublishedURL string     `json:"publishedUrl"`
}

type Duplicate struct {
	SHA256        string `json:"sha256"`
	AssetID       string `json:"assetId"`
	StorageStatus string `json:"storageStatus"`
	Uses          []Use  `json:"uses"`
}

type Schedule struct {
	LocalDateTime string `json:"localDateTime"`
	Timezone      string `json:"timezone,omitempty"`
}

type DestinationInput struct {
	AccountID    string         `json:"accountId"`
	Format       string         `json:"format"`
	Caption      *string        `json:"caption,omitempty"`
	Title        *string        `json:"title,omitempty"`
	Description  *string        `json:"description,omitempty"`
	FirstComment *string        `json:"firstComment,omitempty"`
	Options      map[string]any `json:"options,omitempty"`
	MediaIDs     []string       `json:"mediaIds,omitempty"`
	CoverMediaID string         `json:"coverMediaId,omitempty"`
}

type PostInput struct {
	ProjectID     string             `json:"projectId,omitempty"`
	InternalTitle string             `json:"internalTitle,omitempty"`
	BaseCaption   string             `json:"baseCaption"`
	BaseHashtags  []string           `json:"baseHashtags"`
	LinkURL       string             `json:"linkUrl,omitempty"`
	MediaIDs      []string           `json:"mediaIds"`
	Destinations  []DestinationInput `json:"destinations"`
	Schedule      *Schedule          `json:"schedule,omitempty"`
	Repeat        *Repeat            `json:"repeat,omitempty"`
	AltTexts      map[string]string  `json:"altTexts,omitempty"` // asset id → alt text (ADR-080)
}

type Issue struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Message  string `json:"message"`
}

type Destination struct {
	ID               string     `json:"id"`
	AccountID        string     `json:"accountId"`
	Platform         string     `json:"platform"`
	AccountName      string     `json:"accountName"`
	Format           string     `json:"format"`
	PublishMode      string     `json:"publishMode"`
	FinalText        string     `json:"finalText"`
	MediaIDs         []string   `json:"mediaIds"`
	ScheduledAt      *time.Time `json:"scheduledAt"`
	Status           string     `json:"status"`
	PublishedURL     string     `json:"publishedUrl"`
	PublishedAt      *time.Time `json:"publishedAt"`
	ThreadStatus     string     `json:"threadStatus"` // POSTED, PARTIAL, FAILED (X, Threads; ADR-080)
	ThreadPosted     *int       `json:"threadPosted"`
	ThreadError      string     `json:"threadError"`
	LastErrorMessage string     `json:"lastErrorMessage"`
	Issues           []Issue    `json:"issues"`
	Handoff          string     `json:"handoff"`
}

type MediaItem struct {
	AssetID string `json:"assetId"`
}

type Post struct {
	ID            string        `json:"id"`
	ProjectID     string        `json:"projectId"`
	InternalTitle string        `json:"internalTitle"`
	BaseCaption   string        `json:"baseCaption"`
	ScheduledAt   *time.Time    `json:"scheduledAt"`
	Timezone      string        `json:"timezone"`
	LocalDateTime string        `json:"localDateTime"`
	Status        string        `json:"status"`
	Media         []MediaItem   `json:"media"`
	Destinations  []Destination `json:"destinations"`
	CreatedAt     time.Time     `json:"createdAt"`
	RepeatEvery   int           `json:"repeatEveryDays"`
	RepeatUntil   string        `json:"repeatUntil"`
}

// Title is how a post is named in lists.
func (p Post) Title() string {
	if p.InternalTitle != "" {
		return p.InternalTitle
	}
	t := p.BaseCaption
	if r := []rune(t); len(r) > 60 {
		t = string(r[:60]) + "…"
	}
	if t == "" {
		return "(no text)"
	}
	return t
}

type DestinationValidation struct {
	DestinationID string  `json:"destinationId"`
	Platform      string  `json:"platform"`
	Format        string  `json:"format"`
	AccountName   string  `json:"accountName"`
	Issues        []Issue `json:"issues"`
}

type Validation struct {
	Valid        bool                    `json:"valid"`
	ErrorCount   int                     `json:"errorCount"`
	WarningCount int                     `json:"warningCount"`
	Destinations []DestinationValidation `json:"destinations"`
}

type NextSlot struct {
	ScheduledAt   time.Time `json:"scheduledAt"`
	LocalDateTime string    `json:"localDateTime"`
	Timezone      string    `json:"timezone"`
}

type TikTokCreatorInfo struct {
	Nickname                string   `json:"nickname"`
	Username                string   `json:"username"`
	PrivacyLevelOptions     []string `json:"privacyLevelOptions"`
	MaxVideoPostDurationSec int      `json:"maxVideoPostDurationSec"`
	CommentDisabled         bool     `json:"commentDisabled"`
	DuetDisabled            bool     `json:"duetDisabled"`
	StitchDisabled          bool     `json:"stitchDisabled"`
	Audited                 bool     `json:"audited"`
}

type AIStatus struct {
	Enabled    bool   `json:"enabled"`
	Model      string `json:"model"`
	UsedToday  int    `json:"usedToday"`
	DailyLimit int    `json:"dailyLimit"`
}

type AIRequest struct {
	Action    string `json:"action"`
	Text      string `json:"text"`
	Platform  string `json:"platform,omitempty"`
	MaxLength int    `json:"maxLength,omitempty"`
	Language  string `json:"language,omitempty"`
}

type AIResult struct {
	Suggestions []string `json:"suggestions"`
	Hashtags    []string `json:"hashtags"`
	UsedToday   int      `json:"usedToday"`
	DailyLimit  int      `json:"dailyLimit"`
}

// Template is a saved starting point for a post (website: Compose → templates).
type Template struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Caption      string   `json:"caption"`
	Hashtags     []string `json:"hashtags"`
	LinkURL      string   `json:"linkUrl"`
	FirstComment string   `json:"firstComment"`
	AccountIDs   []string `json:"accountIds"`
}

type AccountGroup struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	AccountIDs []string `json:"accountIds"`
}

// Repeat: a post that comes back every N days (ADR-040), until a day (YYYY-MM-DD) or for good.
type Repeat struct {
	EveryDays int    `json:"everyDays"`
	Until     string `json:"until,omitempty"`
}

type Analytics struct {
	Days           int                         `json:"days"`
	ByPlatform     map[string]map[string]int64 `json:"byPlatform"`
	Totals         map[string]int64            `json:"totals"`
	Top            []TopPost                   `json:"top"`
	Posts          int                         `json:"posts"`
	Range          string                      `json:"range"` // ADR-083 (empty from an older server)
	Timezone       string                      `json:"timezone"`
	Previous       map[string]int64            `json:"previous"`
	Gained         map[string]int64            `json:"gained"`
	GainedPrevious map[string]int64            `json:"gainedPrevious"`
	HistoryFrom    string                      `json:"historyFrom"`
	ByAccount      []AccountTotals             `json:"byAccount"`
	Daily          []AnalyticsDay              `json:"daily"`
	Activity       *Activity                   `json:"activity"`
	Clicks         *Clicks                     `json:"clicks"`
	Coverage       *Coverage                   `json:"coverage"`      // ADR-084
	MetricSources  map[string][]string         `json:"metricSources"` // platforms each number comes from
}

// Coverage: how much of each period the server's snapshots cover (full, partial, none). GainedPrevious is null unless
// the previous period is fully covered: never show a change against numbers that were never recorded.
type Coverage struct {
	Current  string `json:"current"`
	Previous string `json:"previous"`
}

type AccountTotals struct {
	AccountID   string           `json:"accountId"`
	AccountName string           `json:"accountName"`
	Platform    string           `json:"platform"`
	Posts       int              `json:"posts"`
	Totals      map[string]int64 `json:"totals"`
}

type AnalyticsDay struct {
	Day    string           `json:"day"`
	Posts  int              `json:"posts"`
	Gained map[string]int64 `json:"gained"`
}

type Activity struct {
	Published int `json:"published"`
	Failed    int `json:"failed"`
	Scheduled int `json:"scheduled"`
	Manual    int `json:"manual"`
}

type Clicks struct {
	Total    int64            `json:"total"`
	Previous int64            `json:"previous"`
	BySource map[string]int64 `json:"bySource"`
}

type TopPost struct {
	PostID      string           `json:"postId"`
	Title       string           `json:"title"`
	PublishedAt *time.Time       `json:"publishedAt"`
	Totals      map[string]int64 `json:"totals"`
}

type PostMetrics struct {
	PostID       string               `json:"postId"`
	Totals       map[string]int64     `json:"totals"`
	Destinations []DestinationMetrics `json:"destinations"`
}

type DestinationMetrics struct {
	DestinationID string           `json:"destinationId"`
	Platform      string           `json:"platform"`
	AccountName   string           `json:"accountName"`
	Metrics       map[string]int64 `json:"metrics"`
	FetchedAt     *time.Time       `json:"fetchedAt"`
	Error         string           `json:"error"`
	Supported     bool             `json:"supported"`
	Note          string           `json:"note"`
}

type Link struct {
	ID        string           `json:"id"`
	Code      string           `json:"code"`
	ShortURL  string           `json:"shortUrl"`
	TargetURL string           `json:"targetUrl"`
	Campaign  string           `json:"campaign"`
	ProjectID string           `json:"projectId"`
	Clicks    int64            `json:"clicks"`
	BySource  map[string]int64 `json:"bySource"`
	CreatedAt time.Time        `json:"createdAt"`
}

type Notification struct {
	ID            string     `json:"id"`
	EventType     string     `json:"eventType"`
	Severity      string     `json:"severity"`
	Title         string     `json:"title"`
	Body          string     `json:"body"`
	ProjectID     string     `json:"projectId"`
	PostID        string     `json:"postId"`
	DestinationID string     `json:"destinationId"`
	Link          string     `json:"link"`
	ReadAt        *time.Time `json:"readAt"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// AltTextResult: AI alt text for an image (POST /api/ai/alt-text).
type AltTextResult struct {
	AltText    string `json:"altText"`
	UsedToday  int    `json:"usedToday"`
	DailyLimit int    `json:"dailyLimit"`
}

// Billing (ADR-087): the prepaid wallet. Money is micro-USD (1 USD = 1,000,000).
type Billing struct {
	Mode                    string `json:"mode"`
	Exempt                  bool   `json:"exempt"`
	BalanceMicros           int64  `json:"balanceMicros"`
	AvailableMicros         int64  `json:"availableMicros"`
	HeldMicros              int64  `json:"heldMicros"`
	CreditLimitMicros       int64  `json:"creditLimitMicros"`
	DailyCapMicros          *int64 `json:"dailyCapMicros"`
	SpentTodayMicros        int64  `json:"spentTodayMicros"`
	Spent30dMicros          int64  `json:"spent30dMicros"`
	DaysLeft                *int64 `json:"daysLeft"`
	ScheduledEstimateMicros int64  `json:"scheduledEstimateMicros"`
	StorageUsedBytes        int64  `json:"storageUsedBytes"`
	StorageQuotaBytes       int64  `json:"storageQuotaBytes"`
}

type LedgerLine struct {
	Kind         string `json:"kind"`
	AmountMicros int64  `json:"amountMicros"`
	BalanceAfter int64  `json:"balanceAfterMicros"`
	PostID       string `json:"postId"`
	PostTitle    string `json:"postTitle"`
	Platform     string `json:"platform"`
	Format       string `json:"format"`
	Via          string `json:"via"`
	Note         string `json:"note"`
	CreatedAt    string `json:"createdAt"`
}

type Estimate struct {
	Exempt           bool   `json:"exempt"`
	AutomationMicros int64  `json:"automationMicros"`
	TotalMicros      int64  `json:"totalMicros"`
	AvailableMicros  *int64 `json:"availableMicros"`
	Destinations     []struct {
		Platform           string `json:"platform"`
		Format             string `json:"format"`
		Manual             bool   `json:"manual"`
		ServiceMicros      int64  `json:"serviceMicros"`
		PlatformCostMicros int64  `json:"platformCostMicros"`
		FollowUpMicros     int64  `json:"followUpMicros"`
		TotalMicros        int64  `json:"totalMicros"`
	} `json:"destinations"`
}
