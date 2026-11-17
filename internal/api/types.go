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
	Nickname               string   `json:"nickname"`
	Username               string   `json:"username"`
	PrivacyLevelOptions    []string `json:"privacyLevelOptions"`
	MaxVideoPostDurationSec int     `json:"maxVideoPostDurationSec"`
	CommentDisabled        bool     `json:"commentDisabled"`
	DuetDisabled           bool     `json:"duetDisabled"`
	StitchDisabled         bool     `json:"stitchDisabled"`
	Audited                bool     `json:"audited"`
}
