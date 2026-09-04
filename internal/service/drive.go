package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
