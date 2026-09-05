package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type DriveService struct {
	db            *storage.DB
	client        *http.Client
	resourceURL   string
	resourceToken string
}

func NewDriveService(db *storage.DB, resourceURL, resourceToken string) *DriveService {
	if resourceURL == "" {
		resourceURL = "http://127.0.0.1:8100"
	}
	return &DriveService{
		db:            db,
		resourceURL:   resourceURL,
		resourceToken: resourceToken,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func decodeProviderJSON(response *http.Response, operation string, target any) error {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", operation, response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode %s response: %w", operation, err)
	}
	return nil
}

// GetDefaultAccount returns the default 115 account or first available
func (s *DriveService) GetDefaultAccount() (*domain.DriveAccount, error) {
	accounts, err := s.db.ListAccounts()
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		cookie, _ := s.db.GetSetting("115_cookie")
		if cookie == "" {
			return nil, errors.New("no drive accounts configured")
		}
		now := time.Now()
		fallback := domain.DriveAccount{ID: "default", Type: "115", Name: "默认账号", Cookie: cookie, Status: "active", IsDefault: true, CreatedAt: now, UpdatedAt: now}
		if err := s.db.SaveAccount(&fallback); err != nil {
			return nil, err
		}
		return &fallback, nil
	}
	for index := range accounts {
		if accounts[index].IsDefault && accounts[index].Status == "active" {
			return &accounts[index], nil
		}
	}
	for index := range accounts {
		if accounts[index].Status == "active" {
			return &accounts[index], nil
		}
	}
	for index := range accounts {
		if accounts[index].IsDefault {
			return &accounts[index], nil
		}
	}
	return &accounts[0], nil
}

// CheckAccounts refreshes credential, VIP, and storage state for every managed 115 account.
func (s *DriveService) CheckAccounts(ctx context.Context) ([]domain.DriveAccount, error) {
	accounts, err := s.db.ListAccounts()
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		if cookie, _ := s.db.GetSetting("115_cookie"); cookie != "" {
			account, err := s.GetDefaultAccount()
			if err != nil {
				return nil, err
			}
			accounts = []domain.DriveAccount{*account}
		}
	}
	for index := range accounts {
		account := &accounts[index]
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://webapi.115.com/files/index_info", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Cookie", account.Cookie)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36")
		resp, requestErr := s.client.Do(req)
		if requestErr != nil {
			account.Status = "error"
		} else {
			var raw struct {
				State bool   `json:"state"`
				Error string `json:"error"`
				Data  struct {
					VIP       int   `json:"vip"`
					Expire    int64 `json:"expire"`
					SpaceInfo struct {
						Total struct {
							Size int64 `json:"size"`
						} `json:"all_total"`
						Used struct {
							Size int64 `json:"size"`
						} `json:"all_use"`
					} `json:"space_info"`
				} `json:"data"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw)
			resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 || decodeErr != nil || !raw.State {
				account.Status = "error"
			} else {
				account.Status = "active"
				account.VIPLevel = raw.Data.VIP
				account.QuotaTotal = raw.Data.SpaceInfo.Total.Size
				account.QuotaUsed = raw.Data.SpaceInfo.Used.Size
				if raw.Data.Expire > 0 {
					expires := time.Unix(raw.Data.Expire, 0)
					account.VIPExpiresAt = &expires
					if expires.Before(time.Now()) {
						account.Status = "expired"
					}
				}
			}
		}
		if err := s.db.SaveAccount(account); err != nil {
			return nil, err
		}
	}
	return accounts, nil
}

func (s *DriveService) resourceConfig() (string, string) {
	baseURL, _ := s.db.GetSetting("resource_api_url")
	if baseURL == "" {
		baseURL = s.resourceURL
	}
	token, _ := s.db.GetSetting("resource_api_token")
	if token == "" {
		token = s.resourceToken
	}
	return strings.TrimRight(baseURL, "/"), token
}

// ListFiles lists files in a directory for a given account.
func (s *DriveService) ListFiles(accountID, cid string, offset, limit int) ([]domain.DriveFile, int64, error) {
	return s.listFiles(context.Background(), accountID, cid, offset, limit)
}

func (s *DriveService) listFiles(ctx context.Context, accountID, cid string, offset, limit int) ([]domain.DriveFile, int64, error) {
	account, err := s.getAccount(accountID)
	if err != nil {
		return nil, 0, err
	}
	if cid == "" {
		cid = "0"
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if limit > 1000 {
		limit = 1000
	}
	reqURL := fmt.Sprintf("https://webapi.115.com/files?aid=1&cid=%s&o=user_ptime&asc=0&offset=%d&show_dir=1&limit=%d&code=&scid=&snap=0&natsort=1&record_open_time=1&source=&format=json", url.QueryEscape(cid), offset, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Cookie", account.Cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("115 files request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("115 files returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, 0, err
	}
	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
		Count int64  `json:"count"`
		Data  []struct {
			Fid  any    `json:"fid"`
			Cid  any    `json:"cid"`
			Pid  any    `json:"pid"`
			Name string `json:"n"`
			Size any    `json:"s"`
			Pc   string `json:"pc"`
			Sha1 string `json:"sha"`
			T    string `json:"t"`
			Te   string `json:"te"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, fmt.Errorf("parse 115 files: %w", err)
	}
	if !raw.State {
		return nil, 0, fmt.Errorf("115 files rejected: %s", raw.Error)
	}
	files := make([]domain.DriveFile, 0, len(raw.Data))
	for _, item := range raw.Data {
		fileID := fmt.Sprintf("%v", item.Fid)
		isFolder := false
		if fileID == "" || fileID == "0" || fileID == "<nil>" {
			fileID = fmt.Sprintf("%v", item.Cid)
			isFolder = true
		}
		var size int64
		switch value := item.Size.(type) {
		case float64:
			size = int64(value)
		case string:
			size, _ = strconv.ParseInt(value, 10, 64)
		}
		updatedValue := item.Te
		if updatedValue == "" {
			updatedValue = item.T
		}
		var updatedAt time.Time
		if timestamp, err := strconv.ParseInt(updatedValue, 10, 64); err == nil {
			updatedAt = time.Unix(timestamp, 0)
		}
		files = append(files, domain.DriveFile{
			FileID: fileID, ParentID: fmt.Sprintf("%v", item.Pid), Name: item.Name, Size: size,
			PickCode: item.Pc, Sha1: item.Sha1, IsFolder: isFolder, UpdatedTime: updatedAt,
		})
	}
	return files, raw.Count, nil
}

// AddOfflineTask submits a magnet, ed2k, or HTTP URL to 115 offline download.
func (s *DriveService) AddOfflineTask(accountID, urlStr, targetCID string) (string, error) {
	return s.AddOfflineTaskCtx(context.Background(), accountID, urlStr, targetCID)
}

// ValidateOfflineURL accepts only provider-supported offline download schemes.
func ValidateOfflineURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "magnet" && parsed.Scheme != "ed2k" && parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "sha1") {
		return fmt.Errorf("offline URL must use magnet, ed2k, http, https, or sha1")
	}
	return nil
}

// AddOfflineTaskCtx submits one cancellable 115 offline-download request.
func (s *DriveService) AddOfflineTaskCtx(ctx context.Context, accountID, urlStr, targetCID string) (string, error) {
	if err := ValidateOfflineURL(urlStr); err != nil {
		return "", err
	}
	acc, err := s.getAccount(accountID)
	if err != nil {
		return "", err
	}

	if targetCID == "" {
		targetCID = "0"
	}

	form := url.Values{}
	form.Set("url", urlStr)
	form.Set("wp_path_id", targetCID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://115.com/web/lixian/?ct=lixian&ac=add_task_url", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("add offline task: %w", err)
	}
	defer resp.Body.Close()

	var raw struct {
		State    bool   `json:"state"`
		ErrNo    int    `json:"errno"`
		ErrorMsg string `json:"error_msg"`
		InfoHash string `json:"info_hash"`
		Name     string `json:"name"`
		URL      string `json:"url"`
	}
	if err := decodeProviderJSON(resp, "115 offline task", &raw); err != nil {
		return "", err
	}

	if !raw.State {
		return "", fmt.Errorf("115 error %d: %s", raw.ErrNo, raw.ErrorMsg)
	}
	if strings.TrimSpace(raw.InfoHash) == "" {
		return "", fmt.Errorf("115 offline task response did not contain a task ID")
	}

	return raw.InfoHash, nil
}

// ListOfflineTasks fetches the list of active/completed offline tasks
func (s *DriveService) ListOfflineTasks(accountID string) ([]domain.OfflineTask, error) {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", "https://115.com/web/lixian/?ct=lixian&ac=task_lists", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("115 offline list returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}

	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
		Tasks []struct {
			InfoHash string  `json:"info_hash"`
			Name     string  `json:"name"`
			Size     int64   `json:"size"`
			Status   int     `json:"status"`
			Percent  float64 `json:"percentDone"`
			URL      string  `json:"url"`
			FileID   string  `json:"file_id"`
		} `json:"tasks"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if !raw.State {
		return nil, fmt.Errorf("115 offline list rejected: %s", raw.Error)
	}

	result := make([]domain.OfflineTask, 0, len(raw.Tasks))
	for _, task := range raw.Tasks {
		result = append(result, domain.OfflineTask{
			InfoHash: task.InfoHash, Name: task.Name, Size: task.Size, Status: task.Status,
			Percent: task.Percent, URL: task.URL, AccountID: acc.ID, FileID: task.FileID, CreatedAt: time.Now(),
		})
	}
	return result, nil
}

// PartialBatchError reports provider work accepted before a later batch item failed.
type PartialBatchError struct {
	CompletedIDs []string
	Err          error
}

func (e *PartialBatchError) Error() string {
	return fmt.Sprintf("%d offline task(s) accepted before failure: %v", len(e.CompletedIDs), e.Err)
}

func (e *PartialBatchError) Unwrap() error { return e.Err }

// ShareEntry is one entry inside a share snapshot.
type ShareEntry struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size,omitempty"`
	IsDir bool   `json:"is_dir"`
}

var shareURLPattern = regexp.MustCompile(`115(?:cdn)?\.com/s/([A-Za-z0-9]+)`)

// ParseShareCode extracts the share code and receive code (password) from a
// share URL like https://115cdn.com/s/xxxx?password=yyy or a bare share code.
func ParseShareCode(rawURL, password string) (string, string, error) {
	code := ""
	receive := strings.TrimSpace(password)
	if m := shareURLPattern.FindStringSubmatch(rawURL); m != nil {
		code = m[1]
	}
	if code == "" {
		trimmed := strings.TrimSpace(rawURL)
		if regexp.MustCompile(`^[A-Za-z0-9]{4,128}$`).MatchString(trimmed) {
			code = trimmed
		}
	}
	if code == "" {
		return "", "", fmt.Errorf("无法从输入中解析 115 分享码: %s", rawURL)
	}
	if receive == "" {
		if u, err := url.Parse(rawURL); err == nil {
			receive = u.Query().Get("password")
			if receive == "" {
				receive = u.Query().Get("pwd")
			}
		}
	}
	if len(receive) > 128 {
		return "", "", fmt.Errorf("115 提取码过长")
	}
	return code, receive, nil
}

func (s *DriveService) snapshotShareEntries(ctx context.Context, account *domain.DriveAccount, shareCode, receiveCode, cid string) (string, []ShareEntry, error) {
	const pageSize = 1000
	const maxEntries = 50000
	entries := make([]ShareEntry, 0)
	seen := make(map[string]struct{})
	title := ""
	for offset := 0; ; {
		reqURL := fmt.Sprintf("https://webapi.115.com/share/snap?share_code=%s&receive_code=%s&cid=%s&offset=%d&limit=%d", url.QueryEscape(shareCode), url.QueryEscape(receiveCode), url.QueryEscape(cid), offset, pageSize)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return "", nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36")
		req.Header.Set("Referer", "https://115.com/")
		req.Header.Set("Cookie", account.Cookie)
		resp, err := s.client.Do(req)
		if err != nil {
			return "", nil, fmt.Errorf("115 分享预检请求失败: %w", err)
		}
		var raw struct {
			State bool   `json:"state"`
			Error string `json:"error"`
			Count int    `json:"count"`
			Data  struct {
				Count     int `json:"count"`
				ShareInfo struct {
					ShareTitle string `json:"share_title"`
					FileName   string `json:"file_name"`
				} `json:"shareinfo"`
				List []struct {
					Fid any    `json:"fid"`
					Cid any    `json:"cid"`
					N   string `json:"n"`
					S   int64  `json:"s"`
				} `json:"list"`
			} `json:"data"`
		}
		decodeErr := decodeProviderJSON(resp, "115 share snapshot", &raw)
		resp.Body.Close()
		if decodeErr != nil {
			return "", nil, decodeErr
		}
		if !raw.State {
			return "", nil, fmt.Errorf("115 分享预检被拒绝: %s", raw.Error)
		}
		if title == "" {
			title = raw.Data.ShareInfo.ShareTitle
			if title == "" {
				title = raw.Data.ShareInfo.FileName
			}
		}
		for _, item := range raw.Data.List {
			fid := strings.TrimSpace(fmt.Sprint(item.Fid))
			folderID := strings.TrimSpace(fmt.Sprint(item.Cid))
			isDirectory := fid == "" || fid == "<nil>"
			id := fid
			if isDirectory {
				id = folderID
			}
			if id == "" || id == "<nil>" {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			entries = append(entries, ShareEntry{ID: id, Name: item.N, Size: item.S, IsDir: isDirectory})
			if len(entries) > maxEntries {
				return "", nil, fmt.Errorf("115 share contains more than %d top-level entries", maxEntries)
			}
		}
		total := raw.Data.Count
		if total == 0 {
			total = raw.Count
		}
		if total > maxEntries {
			return "", nil, fmt.Errorf("115 share contains %d top-level entries; maximum is %d", total, maxEntries)
		}
		if total > 0 && len(entries) >= total {
			break
		}
		if len(raw.Data.List) < pageSize {
			if total > 0 && len(entries) < total {
				return "", nil, fmt.Errorf("115 share snapshot ended at %d of %d entries", len(entries), total)
			}
			break
		}
		offset += len(raw.Data.List)
	}
	return title, entries, nil
}

// SnapshotShare lists the top-level contents of a 115 share link.
func (s *DriveService) SnapshotShare(accountID, rawURL, password string) (string, []ShareEntry, error) {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return "", nil, err
	}
	code, receive, err := ParseShareCode(rawURL, password)
	if err != nil {
		return "", nil, err
	}
	return s.snapshotShareEntries(context.Background(), acc, code, receive, "0")
}

// SaveShare transfers all top-level files of a 115 share into targetCid.
func (s *DriveService) SaveShare(accountID, rawURL, password, targetCid string) (int, string, error) {
	return s.SaveShareCtx(context.Background(), accountID, rawURL, password, targetCid)
}

// SaveShareCtx transfers all top-level files with cancellation.
func (s *DriveService) SaveShareCtx(ctx context.Context, accountID, rawURL, password, targetCid string) (int, string, error) {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return 0, "", err
	}
	if targetCid == "" {
		targetCid = "0"
	}
	code, receive, err := ParseShareCode(rawURL, password)
	if err != nil {
		return 0, "", err
	}
	title, files, err := s.snapshotShareEntries(ctx, acc, code, receive, "0")
	if err != nil {
		return 0, "", err
	}
	if len(files) == 0 {
		return 0, title, fmt.Errorf("115 分享内容为空或已失效")
	}
	ids := make([]string, 0, len(files))
	for _, f := range files {
		ids = append(ids, f.ID)
	}

	uid := ""
	if m := regexp.MustCompile(`(?:^|;\s*)UID=([^_;]+)`).FindStringSubmatch(acc.Cookie); m != nil {
		uid = m[1]
	}

	form := url.Values{}
	form.Set("share_code", code)
	form.Set("receive_code", receive)
	form.Set("file_id", strings.Join(ids, ","))
	form.Set("cid", targetCid)
	form.Set("user_id", uid)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://webapi.115.com/share/receive", strings.NewReader(form.Encode()))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36")
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, title, fmt.Errorf("115 转存请求失败: %w", err)
	}
	defer resp.Body.Close()
	var raw struct {
		State  bool   `json:"state"`
		Error  string `json:"error"`
		ErrNo  int    `json:"errno"`
		Status string `json:"status"`
	}
	if err := decodeProviderJSON(resp, "115 share receive", &raw); err != nil {
		return 0, title, err
	}
	if !raw.State {
		msg := raw.Error
		if msg == "" {
			msg = raw.Status
		}
		if msg == "" {
			msg = fmt.Sprintf("errno=%d", raw.ErrNo)
		}
		return 0, title, fmt.Errorf("115 转存失败: %s", msg)
	}
	return len(ids), title, nil
}

// ShareLink is a newly generated 115 share URL and its extraction code.
type ShareLink struct {
	URL         string `json:"url"`
	ShareCode   string `json:"share_code"`
	ReceiveCode string `json:"receive_code,omitempty"`
}

// GenerateShareLink creates a 115 share for one file or directory.
func (s *DriveService) GenerateShareLink(ctx context.Context, accountID, fileID string) (ShareLink, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return ShareLink{}, fmt.Errorf("file ID is required")
	}
	acc, err := s.getAccount(accountID)
	if err != nil {
		return ShareLink{}, err
	}
	form := url.Values{
		"file_ids":    {fileID},
		"is_asc":      {"1"},
		"order":       {"file_name"},
		"ignore_warn": {"1"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://webapi.115.com/share/send", strings.NewReader(form.Encode()))
	if err != nil {
		return ShareLink{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36")
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Cookie", acc.Cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		return ShareLink{}, fmt.Errorf("create 115 share: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ShareLink{}, fmt.Errorf("read 115 share response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ShareLink{}, fmt.Errorf("create 115 share returned HTTP %d", resp.StatusCode)
	}
	var raw struct {
		State       bool   `json:"state"`
		Error       string `json:"error"`
		Message     string `json:"message"`
		ShareCode   string `json:"share_code"`
		ReceiveCode string `json:"receive_code"`
		Data        struct {
			ShareCode   string `json:"share_code"`
			ReceiveCode string `json:"receive_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ShareLink{}, fmt.Errorf("decode 115 share response: %w", err)
	}
	if !raw.State {
		message := raw.Error
		if message == "" {
			message = raw.Message
		}
		if message == "" {
			message = "request rejected"
		}
		return ShareLink{}, fmt.Errorf("create 115 share: %s", message)
	}
	shareCode := raw.Data.ShareCode
	if shareCode == "" {
		shareCode = raw.ShareCode
	}
	receiveCode := raw.Data.ReceiveCode
	if receiveCode == "" {
		receiveCode = raw.ReceiveCode
	}
	if shareCode == "" {
		return ShareLink{}, fmt.Errorf("create 115 share: response did not contain a share code")
	}
	shareURL := "https://115.com/s/" + url.PathEscape(shareCode)
	if receiveCode != "" {
		shareURL += "?password=" + url.QueryEscape(receiveCode)
	}
	return ShareLink{URL: shareURL, ShareCode: shareCode, ReceiveCode: receiveCode}, nil
}

// MakeDir creates a new directory in 115.
func (s *DriveService) MakeDir(accountID, pid, name string) (string, error) {
	return s.makeDir(context.Background(), accountID, pid, name)
}

func (s *DriveService) makeDir(ctx context.Context, accountID, pid, name string) (string, error) {
	name = strings.TrimSpace(name)
	pid = strings.TrimSpace(pid)
	if name == "" {
		return "", fmt.Errorf("directory name is required")
	}
	if pid == "" {
		pid = "0"
	}
	account, err := s.getAccount(accountID)
	if err != nil {
		return "", err
	}
	form := url.Values{"pid": {pid}, "cname": {name}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://webapi.115.com/files/add", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", account.Cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var raw struct {
		State bool   `json:"state"`
		Cid   any    `json:"cid"`
		Error string `json:"error"`
	}
	if err := decodeProviderJSON(resp, "115 mkdir", &raw); err != nil {
		return "", err
	}
	if !raw.State {
		return "", fmt.Errorf("mkdir error: %s", raw.Error)
	}
	cid := strings.TrimSpace(fmt.Sprint(raw.Cid))
	if cid == "" || cid == "<nil>" {
		return "", fmt.Errorf("115 mkdir response did not contain a CID")
	}
	return cid, nil
}

// Rename renames a file or directory in 115.
func (s *DriveService) Rename(accountID, fileID, newName string) error {
	return s.rename(context.Background(), accountID, fileID, newName)
}

func (s *DriveService) rename(ctx context.Context, accountID, fileID, newName string) error {
	fileID = strings.TrimSpace(fileID)
	newName = strings.TrimSpace(newName)
	if fileID == "" || newName == "" {
		return fmt.Errorf("file ID and new name are required")
	}
	account, err := s.getAccount(accountID)
	if err != nil {
		return err
	}
	form := url.Values{"fid": {fileID}, "file_name": {newName}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://webapi.115.com/files/edit", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", account.Cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
	}
	if err := decodeProviderJSON(resp, "115 rename", &raw); err != nil {
		return err
	}
	if !raw.State {
		return fmt.Errorf("rename error: %s", raw.Error)
	}
	return nil
}

// Move moves files or folders into a target folder.
func (s *DriveService) Move(accountID string, fileIDs []string, targetPID string) error {
	return s.move(context.Background(), accountID, fileIDs, targetPID)
}

func (s *DriveService) move(ctx context.Context, accountID string, fileIDs []string, targetPID string) error {
	targetPID = strings.TrimSpace(targetPID)
	if targetPID == "" || len(fileIDs) == 0 {
		return fmt.Errorf("file IDs and target CID are required")
	}
	account, err := s.getAccount(accountID)
	if err != nil {
		return err
	}
	form := url.Values{"pid": {targetPID}}
	for index, fileID := range fileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			return fmt.Errorf("file IDs must not be empty")
		}
		form.Set(fmt.Sprintf("fid[%d]", index), fileID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://webapi.115.com/files/move", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", account.Cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
	}
	if err := decodeProviderJSON(resp, "115 move", &raw); err != nil {
		return err
	}
	if !raw.State {
		return fmt.Errorf("move error: %s", raw.Error)
	}
	return nil
}

// Delete removes files or folders in 115.
func (s *DriveService) Delete(accountID string, fileIDs []string) error {
	return s.DeleteCtx(context.Background(), accountID, fileIDs)
}

// DeleteCtx removes files or folders with cancellation.
func (s *DriveService) DeleteCtx(ctx context.Context, accountID string, fileIDs []string) error {
	if len(fileIDs) == 0 {
		return fmt.Errorf("file IDs are required")
	}
	account, err := s.getAccount(accountID)
	if err != nil {
		return err
	}
	form := url.Values{}
	for index, fileID := range fileIDs {
		fileID = strings.TrimSpace(fileID)
		if fileID == "" {
			return fmt.Errorf("file IDs must not be empty")
		}
		form.Set(fmt.Sprintf("fid[%d]", index), fileID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://webapi.115.com/rb/delete", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", account.Cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
	}
	if err := decodeProviderJSON(resp, "115 delete", &raw); err != nil {
		return err
	}
	if !raw.State {
		return fmt.Errorf("delete error: %s", raw.Error)
	}
	return nil
}

func (s *DriveService) getAccount(id string) (*domain.DriveAccount, error) {
	if id == "" {
		return s.GetDefaultAccount()
	}
	accs, err := s.db.ListAccounts()
	if err != nil {
		return nil, err
	}
	for i := range accs {
		if accs[i].ID == id {
			return &accs[i], nil
		}
	}
	return nil, fmt.Errorf("115 account %s was not found", id)
}

// Resource indexing integration
func (s *DriveService) resourceGet(path string, params url.Values) (map[string]any, error) {
	return s.resourceGetCtx(context.Background(), path, params)
}

func (s *DriveService) resourceGetCtx(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	baseURL, token := s.resourceConfig()
	endpoint := baseURL + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("resource API %s returned HTTP %d", path, resp.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
		return nil, err
	}
	message := fmt.Sprint(result["message"])
	if message == "<nil>" || message == "" {
		message = fmt.Sprint(result["error"])
	}
	if code, present := result["code"]; present && fmt.Sprint(code) != "0" {
		return nil, fmt.Errorf("resource API %s returned code %v: %s", path, code, message)
	}
	if success, present := result["success"].(bool); present && !success {
		return nil, fmt.Errorf("resource API %s rejected request: %s", path, message)
	}
	if state, present := result["state"].(bool); present && !state {
		return nil, fmt.Errorf("resource API %s rejected request: %s", path, message)
	}
	return result, nil
}

func (s *DriveService) GetHomeSummary() (map[string]any, error) {
	return s.resourceGet("/api/v1/home/summary", nil)
}

func (s *DriveService) GetSources() (map[string]any, error) {
	return s.resourceGet("/api/v1/sources", nil)
}

func (s *DriveService) GetTrends() (map[string]any, error) {
	result, err := s.resourceGet("/api/v1/trends", nil)
	if err != nil {
		return nil, err
	}
	if data, ok := result["data"].(map[string]any); ok {
		return data, nil
	}
	return result, nil
}

func (s *DriveService) SearchResources(params url.Values) (map[string]any, error) {
	return s.SearchResourcesCtx(context.Background(), params)
}

// SearchResourcesCtx searches the resource index with cancellation.
func (s *DriveService) SearchResourcesCtx(ctx context.Context, params url.Values) (map[string]any, error) {
	return s.resourceGetCtx(ctx, "/search", params)
}

func (s *DriveService) GetLink(id string) (map[string]any, error) {
	return s.resourceGet("/api/v1/links/"+url.PathEscape(id), nil)
}

func (s *DriveService) AddOfflineTasks(ctx context.Context, accountID string, urls []string, targetCID string) ([]string, error) {
	ids := make([]string, 0, len(urls))
	for _, rawURL := range urls {
		id, err := s.AddOfflineTaskCtx(ctx, accountID, rawURL, targetCID)
		if err != nil {
			return ids, &PartialBatchError{CompletedIDs: append([]string(nil), ids...), Err: err}
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *DriveService) Mkdir(accountID, pid, name string) (string, error) {
	return s.MakeDir(accountID, pid, name)
}

func (s *DriveService) ListFilesCtx(ctx context.Context, accountID, cid string) ([]domain.DriveFile, error) {
	files, _, err := s.listFiles(ctx, accountID, cid, 0, 200)
	return files, err
}

// ListFilesPageCtx returns one bounded 115 directory page and provider total.
func (s *DriveService) ListFilesPageCtx(ctx context.Context, accountID, cid string, offset, limit int) ([]domain.DriveFile, int64, error) {
	return s.listFiles(ctx, accountID, cid, offset, limit)
}

func (s *DriveService) MkdirCtx(ctx context.Context, accountID, pid, name string) (string, error) {
	return s.makeDir(ctx, accountID, pid, name)
}

func (s *DriveService) RenameCtx(ctx context.Context, accountID, fileID, newName string) error {
	return s.rename(ctx, accountID, fileID, newName)
}

func (s *DriveService) MoveCtx(ctx context.Context, accountID string, fileIDs []string, targetCID string) error {
	return s.move(ctx, accountID, fileIDs, targetCID)
}

func (s *DriveService) ImportLink(accountID, linkID string, targetCid string) (string, error) {
	link, err := s.GetLink(linkID)
	if err != nil {
		return "", err
	}
	data, ok := link["data"].(map[string]any)
	if !ok {
		data = link
	}
	rawURL, _ := data["url"].(string)
	if rawURL == "" {
		return "", errors.New("no download url found for link")
	}
	return s.AddOfflineTask(accountID, rawURL, targetCid)
}
