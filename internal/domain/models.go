package domain

import (
	"time"
)

// DriveAccount represents a cloud drive account (115, aliyun, etc.)
type DriveAccount struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"` // "115", "aliyun", "quark"
	Name         string     `json:"name"`
	Cookie       string     `json:"-"`
	Token        string     `json:"-"`
	IsDefault    bool       `json:"is_default"`
	Status       string     `json:"status"` // "active", "expired", "error"
	QuotaUsed    int64      `json:"quota_used"`
	QuotaTotal   int64      `json:"quota_total"`
	VIPLevel     int        `json:"vip_level"`
	VIPExpiresAt *time.Time `json:"vip_expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// DriveFile represents a remote file or folder in 115 or other drives
type DriveFile struct {
	FileID      string    `json:"file_id"`
	ParentID    string    `json:"parent_id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	PickCode    string    `json:"pick_code,omitempty"`
	Sha1        string    `json:"sha1,omitempty"`
	IsFolder    bool      `json:"is_folder"`
	UpdatedTime time.Time `json:"updated_time"`
}

// OfflineTask represents an offline download task (magnet, ed2k, http)
type OfflineTask struct {
	InfoHash  string    `json:"info_hash"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Status    int       `json:"status"` // 0: init, 1: downloading, 2: completed, -1: failed
	Percent   float64   `json:"percent"`
	URL       string    `json:"url"`
	AccountID string    `json:"account_id"`
	FileID    string    `json:"file_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CloudDriveMount reports one configured CloudDrive2 mount and live filesystem capacity.
type CloudDriveMount struct {
	Name       string    `json:"name,omitempty"`
	MountPath  string    `json:"mount_path"`
	RemotePath string    `json:"remote_path,omitempty"`
	Status     string    `json:"status"`
	ReadOnly   *bool     `json:"read_only,omitempty"`
	AutoMount  *bool     `json:"auto_mount,omitempty"`
	TotalSpace int64     `json:"total_space"`
	FreeSpace  int64     `json:"free_space"`
	Error      string    `json:"error,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// EmbyLibrary represents an Emby media library
type EmbyLibrary struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Collection string   `json:"collection_type"` // "movies", "tvshows"
	Locations  []string `json:"locations"`
}

// EmbyMediaItem represents a movie or episode in Emby
type EmbyMediaItem struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"` // "Movie", "Series", "Season", "Episode"
	Path         string            `json:"path"`
	PremiereDate string            `json:"premiere_date,omitempty"`
	HasPoster    bool              `json:"has_poster"`
	HasBackdrop  bool              `json:"has_backdrop"`
	ProviderIDs  map[string]string `json:"provider_ids,omitempty"`
}

// EmbyPlaybackSession represents an active streaming session on Emby
type EmbyPlaybackSession struct {
	ID             string `json:"id"`
	UserName       string `json:"user_name"`
	ItemName       string `json:"item_name"`
	ItemType       string `json:"item_type"`
	Client         string `json:"client"`
	DeviceName     string `json:"device_name"`
	PlayState      string `json:"play_state"` // "Playing", "Paused", "Idle"
	PositionTicks  int64  `json:"position_ticks"`
	PlaybackMethod string `json:"playback_method"` // "DirectPlay", "DirectStream", "Transcode"
}

// ResourceLink represents an indexed resource link from gaotao.cc:8100
type ResourceLink struct {
	ID                int64    `json:"id"`
	DiskType          string   `json:"disk_type"`
	Title             string   `json:"title"`
	URL               string   `json:"url"`
	Password          string   `json:"password,omitempty"`
	FirstSource       string   `json:"first_source"`
	SourceCount       int      `json:"source_count"`
	LastSeenAt        string   `json:"last_seen_at"`
	LatestMessageTime string   `json:"latest_message_time"`
	SourceChannels    []string `json:"source_channels"`
	HealthStatus      string   `json:"health_status"`
	HealthCheckedAt   string   `json:"health_checked_at"`
	HealthHTTPStatus  int      `json:"health_http_status"`
	HealthReason      string   `json:"health_reason"`
	SavedAt           string   `json:"saved_at,omitempty"`
}

// SearchTrend represents search trends from the resource index
type SearchTrend struct {
	Keyword string `json:"keyword"`
	Hits    int    `json:"hits"`
}

// HomeSummary represents the overall statistics
type HomeSummary struct {
	Links        int64  `json:"links"`
	HealthValid  int64  `json:"health_valid"`
	TodayUpdated int64  `json:"today_updated"`
	SourceCount  int64  `json:"source_count"`
	LastUpdated  string `json:"last_updated"`
}

// ScheduledTask represents an async background task or recurring job
type ScheduledTask struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"` // "refresh_library", "poster_cleanup", "sync_offline", "health_check"
	Name      string     `json:"name"`
	CronExpr  string     `json:"cron_expr,omitempty"`
	Enabled   bool       `json:"enabled"`
	Status    string     `json:"status"` // "pending", "running", "completed", "failed", "paused", "idle"
	Progress  float64    `json:"progress"`
	Params    string     `json:"params,omitempty"`
	Result    string     `json:"result,omitempty"`
	Error     string     `json:"error,omitempty"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TaskRun records one execution attempt and its observable log messages.
type TaskRun struct {
	ID          int64      `json:"id"`
	TaskID      string     `json:"task_id"`
	Attempt     int        `json:"attempt"`
	Status      string     `json:"status"`
	Progress    float64    `json:"progress"`
	Logs        []string   `json:"logs"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// AsyncTask represents an asynchronous job executed by the persistent queue.
type AsyncTask struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Payload     map[string]any `json:"payload,omitempty"`
	Status      string         `json:"status"`
	Progress    float64        `json:"progress"`
	Result      string         `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// AgentToken represents an authentication token for AI Agent access
type AgentToken struct {
	ID         string     `json:"id"`
	Token      string     `json:"-"` // SHA-256 digest of the bearer token
	Name       string     `json:"name"`
	Role       string     `json:"role"` // "admin", "agent", "readonly"
	Scopes     []string   `json:"scopes"`
	RateLimit  int        `json:"rate_limit"` // requests per min
	Enabled    bool       `json:"enabled"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// AuditLog represents an action log for compliance and agent tracing
// AuditLog records one UI or Agent operation without storing secret values.
type AuditLog struct {
	ID        int64     `json:"id"`
	Caller    string    `json:"caller"`
	TokenID   string    `json:"token_id,omitempty"`
	AgentName string    `json:"agent_name,omitempty"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Input     string    `json:"input,omitempty"`
	Output    string    `json:"output,omitempty"`
	Status    string    `json:"status"`
	LatencyMS int64     `json:"latency_ms"`
	IP        string    `json:"ip,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SystemConfig represents a system key-value setting
type SystemConfig struct {
	Key         string    `json:"key"`
	Value       string    `json:"value"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}
