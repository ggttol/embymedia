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
	"sync"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

type DriveService struct {
	db            *storage.DB
	client        *http.Client
	resourceURL   string
	resourceToken string
	mu            sync.RWMutex
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

// GetDefaultAccount returns the default 115 account or first available
func (s *DriveService) GetDefaultAccount() (*domain.DriveAccount, error) {
	accs, err := s.db.ListAccounts()
	if err != nil {
		return nil, err
	}
	if len(accs) == 0 {
		cookie, _ := s.db.GetSetting("115_cookie")
		if cookie != "" {
			now := time.Now()
			fallback := domain.DriveAccount{
				ID:        "default",
				Name:      "默认账号",
				Cookie:    cookie,
				Status:    "active",
				IsDefault: true,
				CreatedAt: now,
				UpdatedAt: now,
			}
			_ = s.db.SaveAccount(&fallback)
			return &fallback, nil
		}
		return nil, errors.New("no drive accounts configured")
	}
	for _, a := range accs {
		if a.IsDefault {
			return &a, nil
		}
	}
	return &accs[0], nil
}

// ListFiles lists files in a directory for a given account
func (s *DriveService) ListFiles(accountID string, cid string, offset, limit int) ([]domain.DriveFile, int64, error) {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return nil, 0, err
	}

	if cid == "" {
		cid = "0"
	}
	if limit <= 0 {
		limit = 50
	}

	reqURL := fmt.Sprintf("https://webapi.115.com/files?aid=1&cid=%s&o=user_ptime&asc=0&offset=%d&show_dir=1&limit=%d&code=&scid=&snap=0&natsort=1&record_open_time=1&source=&format=json", cid, offset, limit)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("115 files request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	var raw struct {
		State bool `json:"state"`
		Count int64 `json:"count"`
		Data  []struct {
			Fid   any    `json:"fid"`
			Cid   any    `json:"cid"`
			Pid   any    `json:"pid"`
			Name  string `json:"n"`
			Size  any    `json:"s"`
			Pc    string `json:"pc"`
			Sha1  string `json:"sha"`
			T     string `json:"t"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, 0, fmt.Errorf("parse 115 files: %w", err)
	}

	var files []domain.DriveFile
	for _, item := range raw.Data {
		fileID := fmt.Sprintf("%v", item.Fid)
		isFolder := false
		if fileID == "" || fileID == "0" || fileID == "<nil>" {
			fileID = fmt.Sprintf("%v", item.Cid)
			isFolder = true
		}

		var size int64
		switch v := item.Size.(type) {
		case float64:
			size = int64(v)
		case string:
			size, _ = strconv.ParseInt(v, 10, 64)
		}

		files = append(files, domain.DriveFile{
			FileID:   fileID,
			ParentID: fmt.Sprintf("%v", item.Pid),
			Name:     item.Name,
			Size:     size,
			PickCode: item.Pc,
			Sha1:     item.Sha1,
			IsFolder: isFolder,
		})
	}

	return files, raw.Count, nil
}

// AddOfflineTask submits a magnet / ed2k / http url to 115 offline download
func (s *DriveService) AddOfflineTask(accountID string, urlStr string, targetCid string) (string, error) {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return "", err
	}

	if targetCid == "" {
		targetCid = "0"
	}

	form := url.Values{}
	form.Set("url", urlStr)
	form.Set("wp_path_id", targetCid)

	req, err := http.NewRequest("POST", "https://115.com/web/lixian/?ct=lixian&ac=add_task_url", strings.NewReader(form.Encode()))
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var raw struct {
		State    bool   `json:"state"`
		ErrNo    int    `json:"errno"`
		ErrorMsg string `json:"error_msg"`
		InfoHash string `json:"info_hash"`
		Name     string `json:"name"`
		URL      string `json:"url"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("parse offline resp: %w", err)
	}

	if !raw.State {
		return "", fmt.Errorf("115 error %d: %s", raw.ErrNo, raw.ErrorMsg)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var raw struct {
		State bool `json:"state"`
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

	var res []domain.OfflineTask
	for _, t := range raw.Tasks {
		res = append(res, domain.OfflineTask{
			InfoHash:  t.InfoHash,
			Name:      t.Name,
			Size:      t.Size,
			Status:    t.Status,
			Percent:   t.Percent,
			URL:       t.URL,
			AccountID: acc.ID,
			FileID:    t.FileID,
			CreatedAt: time.Now(),
		})
	}

	return res, nil
}

// ShareEntry is one entry inside a share snapshot.
type ShareEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size,omitempty"`
	IsDir     bool   `json:"is_dir"`
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
		if regexp.MustCompile(`^[A-Za-z0-9_-]{4,128}$`).MatchString(trimmed) {
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

func (s *DriveService) snapshotShareEntries(acc *domain.DriveAccount, shareCode, receiveCode, cid string) (string, []ShareEntry, error) {
	reqURL := fmt.Sprintf("https://webapi.115.com/share/snap?share_code=%s&receive_code=%s&cid=%s&offset=0&limit=1150", url.QueryEscape(shareCode), url.QueryEscape(receiveCode), url.QueryEscape(cid))
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36")
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("115 分享预检请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
		Data  struct {
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
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", nil, fmt.Errorf("115 分享预检响应解析失败: %w", err)
	}
	if !raw.State {
		return "", nil, fmt.Errorf("115 分享预检被拒绝: %s", raw.Error)
	}
	title := raw.Data.ShareInfo.ShareTitle
	if title == "" {
		title = raw.Data.ShareInfo.FileName
	}
	files := make([]ShareEntry, 0, len(raw.Data.List))
	for _, item := range raw.Data.List {
		fid := fmt.Sprintf("%v", item.Fid)
		cidStr := fmt.Sprintf("%v", item.Cid)
		isDir := fid == "" || fid == "<nil>"
		id := fid
		if isDir {
			id = cidStr
		}
		if id == "" || id == "<nil>" {
			continue
		}
		files = append(files, ShareEntry{ID: id, Name: item.N, Size: item.S, IsDir: isDir})
	}
	return title, files, nil
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
	return s.snapshotShareEntries(acc, code, receive, "0")
}

// SaveShare transfers all top-level files of a 115 share into targetCid.
func (s *DriveService) SaveShare(accountID, rawURL, password, targetCid string) (int, string, error) {
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
	title, files, err := s.snapshotShareEntries(acc, code, receive, "0")
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

	req, err := http.NewRequest("POST", "https://webapi.115.com/share/receive", strings.NewReader(form.Encode()))
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, title, err
	}
	var raw struct {
		State  bool   `json:"state"`
		Error  string `json:"error"`
		ErrNo  int    `json:"errno"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, title, fmt.Errorf("115 转存响应解析失败: %w", err)
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

// MakeDir creates a new directory in 115
func (s *DriveService) MakeDir(accountID string, pid string, name string) (string, error) {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("pid", pid)
	form.Set("cname", name)

	req, err := http.NewRequest("POST", "https://webapi.115.com/files/add", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw struct {
		State bool   `json:"state"`
		Cid   any    `json:"cid"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", err
	}
	if !raw.State {
		return "", fmt.Errorf("mkdir error: %s", raw.Error)
	}

	return fmt.Sprintf("%v", raw.Cid), nil
}

// Rename renames a file or directory in 115
func (s *DriveService) Rename(accountID string, fileID string, newName string) error {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return err
	}

	form := url.Values{}
	form.Set("fid", fileID)
	form.Set("file_name", newName)

	req, err := http.NewRequest("POST", "https://webapi.115.com/files/edit", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	if !raw.State {
		return fmt.Errorf("rename error: %s", raw.Error)
	}

	return nil
}

// Move moves files or folders into a target folder
func (s *DriveService) Move(accountID string, fileIDs []string, targetPid string) error {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return err
	}

	form := url.Values{}
	form.Set("pid", targetPid)
	for i, fid := range fileIDs {
		form.Set(fmt.Sprintf("fid[%d]", i), fid)
	}

	req, err := http.NewRequest("POST", "https://webapi.115.com/files/move", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	if !raw.State {
		return fmt.Errorf("move error: %s", raw.Error)
	}

	return nil
}

// Delete removes files or folders in 115
func (s *DriveService) Delete(accountID string, fileIDs []string) error {
	acc, err := s.getAccount(accountID)
	if err != nil {
		return err
	}

	form := url.Values{}
	for i, fid := range fileIDs {
		form.Set(fmt.Sprintf("fid[%d]", i), fid)
	}

	req, err := http.NewRequest("POST", "https://webapi.115.com/rb/delete", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Cookie", acc.Cookie)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var raw struct {
		State bool   `json:"state"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
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
	for _, a := range accs {
		if a.ID == id {
			return &a, nil
		}
	}
	return s.GetDefaultAccount()
}

// Resource indexing integration
func (s *DriveService) GetHomeSummary() (map[string]any, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/home/summary", s.resourceURL), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *DriveService) GetSources() (map[string]any, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/sources", s.resourceURL), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *DriveService) GetTrends() (map[string]any, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/trends", s.resourceURL), nil)
	if err != nil {
		return nil, err
	}
	if s.resourceToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.resourceToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	// If the upstream returned wrapped {"code": 0, "data": {"trends": [...]}}
	if data, ok := res["data"].(map[string]any); ok {
		return data, nil
	}
	return res, nil
}

func (s *DriveService) SearchResources(params url.Values) (map[string]any, error) {
	reqURL := fmt.Sprintf("%s/search?%s", s.resourceURL, params.Encode())
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if s.resourceToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.resourceToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *DriveService) GetLink(id string) (map[string]any, error) {
	reqURL := fmt.Sprintf("%s/api/v1/links/%s", s.resourceURL, id)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if s.resourceToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.resourceToken)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res, nil
}


func (s *DriveService) AddOfflineTasks(ctx context.Context, accountID string, urls []string, targetCID string) ([]string, error) {
	var ids []string
	for _, u := range urls {
		id, err := s.AddOfflineTask(accountID, u, targetCID)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
func (s *DriveService) Mkdir(accountID, pid, name string) (string, error) {
	return s.MakeDir(accountID, pid, name)
}

func (s *DriveService) ListFilesCtx(ctx context.Context, accountID, cid string) ([]domain.DriveFile, error) {
	files, _, err := s.ListFiles(accountID, cid, 0, 200)
	return files, err
}

func (s *DriveService) MkdirCtx(ctx context.Context, accountID, pid, name string) (string, error) {
	return s.MakeDir(accountID, pid, name)
}

func (s *DriveService) RenameCtx(ctx context.Context, accountID, fileID, newName string) error {
	return s.Rename(accountID, fileID, newName)
}

func (s *DriveService) MoveCtx(ctx context.Context, accountID string, fileIDs []string, targetCID string) error {
	return s.Move(accountID, fileIDs, targetCID)
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
