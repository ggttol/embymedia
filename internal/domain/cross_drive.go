package domain

import "time"

type CrossDriveImport struct {
	TaskID          string    `json:"task_id"`
	PriorTaskID     string    `json:"prior_task_id,omitempty"`
	Phase           string    `json:"phase"`
	QuarkAccountID  string    `json:"quark_account_id"`
	SavedRootIDs    []string  `json:"-"`
	QuarkTargetID   string    `json:"quark_target_id"`
	C115AccountID   string    `json:"c115_account_id"`
	DestinationCID  string    `json:"destination_cid,omitempty"`
	TotalFiles      int       `json:"total_files"`
	CompletedFiles  int       `json:"completed_files"`
	TotalBytes      int64     `json:"total_bytes"`
	CompletedBytes  int64     `json:"completed_bytes"`
	CurrentFile     string    `json:"current_file,omitempty"`
	CancelRequested bool      `json:"cancel_requested"`
	Error           string    `json:"error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CrossDriveItem struct {
	ID                int64     `json:"id"`
	TaskID            string    `json:"task_id"`
	SourceFileID      string    `json:"source_file_id"`
	SourceRevision    string    `json:"source_revision,omitempty"`
	RelativePath      string    `json:"relative_path"`
	Name              string    `json:"name"`
	Size              int64     `json:"size"`
	SHA1              string    `json:"sha1,omitempty"`
	PreSHA1           string    `json:"pre_sha1,omitempty"`
	State             string    `json:"state"`
	SpoolPath         string    `json:"-"`
	DownloadedBytes   int64     `json:"downloaded_bytes"`
	DestinationParent string    `json:"destination_parent,omitempty"`
	DestinationID     string    `json:"destination_id,omitempty"`
	UploadID          string    `json:"-"`
	UploadBucket      string    `json:"-"`
	UploadObject      string    `json:"-"`
	Error             string    `json:"error,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type CrossDriveUploadPart struct {
	ItemID     int64  `json:"item_id"`
	PartNumber int    `json:"part_number"`
	ETag       string `json:"-"`
	Size       int64  `json:"size"`
}

type CrossDriveImportDetail struct {
	Import CrossDriveImport `json:"import"`
	Items  []CrossDriveItem `json:"items"`
}
