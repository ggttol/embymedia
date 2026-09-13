package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

const (
	quarkDefaultBaseURL = "https://drive.quark.cn/1/clouddrive"
	quarkMaxEntries     = 10_000
	quarkMaxDepth       = 20
)

type ProviderQuark struct {
	client  *http.Client
	baseURL string
	sleep   func(context.Context, time.Duration) error
}

func NewProviderQuark(client *http.Client) *ProviderQuark {
	return &ProviderQuark{client: client, baseURL: quarkDefaultBaseURL, sleep: sleepContext}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *ProviderQuark) Name() string { return "quark" }
func (p *ProviderQuark) Capabilities() DriveCapabilities {
	return DriveCapabilities{Browse: true, Search: true, Mkdir: true, Rename: true, Move: true, Delete: true, ShareSave: true, Download: true}
}

type quarkEnvelope struct {
	Status   any             `json:"status"`
	Code     any             `json:"code"`
	Message  string          `json:"message"`
	Data     json.RawMessage `json:"data"`
	Metadata struct {
		Total any `json:"_total"`
	} `json:"metadata"`
}

func quarkScalar(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
	case bool:
		if value {
			return "true"
		}
		return "false"
	}
	return ""
}
func quarkAccepted(envelope quarkEnvelope) bool {
	for _, value := range []any{envelope.Code, envelope.Status} {
		text := strings.ToLower(quarkScalar(value))
		if text != "" && text != "0" && text != "200" && text != "ok" && text != "success" {
			return false
		}
	}
	return true
}

func (p *ProviderQuark) request(ctx context.Context, account *domain.DriveAccount, method, path string, query url.Values, body any, target any) error {
	if strings.TrimSpace(account.Cookie) == "" {
		return fmt.Errorf("quark account cookie is empty")
	}
	endpoint := strings.TrimRight(p.baseURL, "/") + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 4; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		request.Header.Set("Cookie", account.Cookie)
		request.Header.Set("Origin", "https://pan.quark.cn")
		request.Header.Set("Referer", "https://pan.quark.cn/")
		request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36")
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := p.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("quark %s: %w", strings.TrimPrefix(path, "/"), err)
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			response.Body.Close()
			if attempt == 3 {
				return &ProviderHTTPError{Operation: "quark " + strings.TrimPrefix(path, "/"), StatusCode: response.StatusCode}
			}
			if err := p.sleep(ctx, time.Duration(1<<attempt)*250*time.Millisecond); err != nil {
				return err
			}
			continue
		}
		var envelope quarkEnvelope
		decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
		decoder.UseNumber()
		decodeErr := decoder.Decode(&envelope)
		closeErr := response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return &ProviderHTTPError{Operation: "quark " + strings.TrimPrefix(path, "/"), StatusCode: response.StatusCode}
		}
		if decodeErr != nil {
			return fmt.Errorf("decode quark %s response: %w", strings.TrimPrefix(path, "/"), decodeErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if !quarkAccepted(envelope) {
			message := strings.TrimSpace(envelope.Message)
			if message == "" {
				message = "request rejected"
			}
			return fmt.Errorf("quark %s rejected: %s", strings.TrimPrefix(path, "/"), message)
		}
		if target != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
			if err := json.Unmarshal(envelope.Data, target); err != nil {
				return fmt.Errorf("decode quark %s data: %w", strings.TrimPrefix(path, "/"), err)
			}
		}
		if metadata, ok := target.(*quarkListData); ok {
			metadata.Total = envelope.Metadata.Total
		}
		return nil
	}
	return fmt.Errorf("quark %s retry exhausted", strings.TrimPrefix(path, "/"))
}

func (p *ProviderQuark) CheckAccount(ctx context.Context, account *domain.DriveAccount) error {
	_, _, err := p.ListFiles(ctx, account, "0", 0, 1)
	return err
}

type quarkFile struct {
	FID       any    `json:"fid"`
	ParentFID any    `json:"pdir_fid"`
	Name      string `json:"file_name"`
	Size      any    `json:"size"`
	FileSize  any    `json:"file_size"`
	Dir       any    `json:"dir"`
	UpdatedAt any    `json:"updated_at"`
	Revision  any    `json:"revision"`
	SHA1      string `json:"sha1"`
}
type quarkListData struct {
	List  []quarkFile `json:"list"`
	Total any         `json:"-"`
}

func parseQuarkFile(item quarkFile, expectedParent string) (domain.DriveFile, error) {
	id := quarkScalar(item.FID)
	parent := quarkScalar(item.ParentFID)
	if parent == "" {
		parent = "0"
	}
	name := strings.TrimSpace(item.Name)
	if id == "" || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return domain.DriveFile{}, fmt.Errorf("quark file response contains an invalid object")
	}
	if expectedParent != "" && parent != expectedParent {
		return domain.DriveFile{}, fmt.Errorf("quark object %s belongs to parent %s, not %s", id, parent, expectedParent)
	}
	isFolder := quarkScalar(item.Dir) == "1" || strings.EqualFold(quarkScalar(item.Dir), "true")
	sizeText := quarkScalar(item.FileSize)
	if sizeText == "" {
		sizeText = quarkScalar(item.Size)
	}
	var size int64
	if !isFolder {
		var err error
		size, err = strconv.ParseInt(sizeText, 10, 64)
		if err != nil || size < 0 {
			return domain.DriveFile{}, fmt.Errorf("quark object %s has an invalid file size", id)
		}
	}
	var updated time.Time
	if stamp, err := strconv.ParseInt(quarkScalar(item.UpdatedAt), 10, 64); err == nil {
		if stamp > 10_000_000_000 {
			stamp /= 1000
		}
		updated = time.Unix(stamp, 0)
	}
	return domain.DriveFile{FileID: id, ParentID: parent, Name: name, Size: size, Sha1: strings.ToUpper(item.SHA1), IsFolder: isFolder, UpdatedTime: updated, Revision: quarkScalar(item.Revision)}, nil
}

func (p *ProviderQuark) ListFiles(ctx context.Context, account *domain.DriveAccount, parent string, offset, limit int) ([]domain.DriveFile, int64, error) {
	if parent == "" {
		parent = "0"
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	page := offset/limit + 1
	query := url.Values{"pr": {"ucpro"}, "fr": {"pc"}, "pdir_fid": {parent}, "_page": {strconv.Itoa(page)}, "_size": {strconv.Itoa(limit)}, "_fetch_total": {"1"}, "_sort": {"file_type:asc,updated_at:desc"}}
	var data quarkListData
	if err := p.request(ctx, account, http.MethodGet, "/file/sort", query, nil, &data); err != nil {
		return nil, 0, err
	}
	files := make([]domain.DriveFile, 0, len(data.List))
	seen := make(map[string]struct{}, len(data.List))
	for _, item := range data.List {
		file, err := parseQuarkFile(item, parent)
		if err != nil {
			return nil, 0, err
		}
		if _, duplicate := seen[file.FileID]; duplicate {
			return nil, 0, fmt.Errorf("quark file page repeated object %s", file.FileID)
		}
		seen[file.FileID] = struct{}{}
		files = append(files, file)
	}
	total, err := strconv.ParseInt(quarkScalar(data.Total), 10, 64)
	if err != nil {
		total = int64(offset + len(files))
	}
	if total < int64(offset+len(files)) {
		return nil, 0, fmt.Errorf("quark file response has an invalid total")
	}
	return files, total, nil
}

func (p *ProviderQuark) SearchFiles(ctx context.Context, account *domain.DriveAccount, queryText string, offset, limit int) ([]domain.DriveFile, int64, error) {
	queryText = strings.TrimSpace(queryText)
	if queryText == "" {
		return nil, 0, fmt.Errorf("search query is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	query := url.Values{"pr": {"ucpro"}, "fr": {"pc"}, "q": {queryText}, "_page": {strconv.Itoa(offset/limit + 1)}, "_size": {strconv.Itoa(limit)}, "_fetch_total": {"1"}}
	var data quarkListData
	if err := p.request(ctx, account, http.MethodGet, "/file/search", query, nil, &data); err != nil {
		return nil, 0, err
	}
	files := make([]domain.DriveFile, 0, len(data.List))
	for _, item := range data.List {
		file, err := parseQuarkFile(item, "")
		if err != nil {
			return nil, 0, err
		}
		files = append(files, file)
	}
	total, err := strconv.ParseInt(quarkScalar(data.Total), 10, 64)
	if err != nil {
		total = int64(offset + len(files))
	}
	return files, total, nil
}

func (p *ProviderQuark) Mkdir(ctx context.Context, account *domain.DriveAccount, parent, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\x00") {
		return "", fmt.Errorf("directory name is invalid")
	}
	if parent == "" {
		parent = "0"
	}
	var data struct {
		FID any `json:"fid"`
	}
	if err := p.request(ctx, account, http.MethodPost, "/file", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, map[string]any{"pdir_fid": parent, "file_name": name, "dir_path": "", "dir_init_lock": false}, &data); err != nil {
		return "", err
	}
	id := quarkScalar(data.FID)
	if id == "" {
		return "", fmt.Errorf("quark mkdir response did not contain an ID")
	}
	return id, nil
}
func (p *ProviderQuark) Rename(ctx context.Context, account *domain.DriveAccount, id, name string) error {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	if id == "" || name == "" || strings.ContainsAny(name, "/\x00") {
		return fmt.Errorf("file ID and valid new name are required")
	}
	return p.request(ctx, account, http.MethodPost, "/file/rename", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, map[string]any{"fid": id, "file_name": name}, nil)
}
func (p *ProviderQuark) Move(ctx context.Context, account *domain.DriveAccount, ids []string, parent string) error {
	if len(ids) == 0 {
		return fmt.Errorf("file IDs are required")
	}
	if parent == "" {
		parent = "0"
	}
	clean := make([]string, len(ids))
	for i, id := range ids {
		clean[i] = strings.TrimSpace(id)
		if clean[i] == "" {
			return fmt.Errorf("file IDs must not be empty")
		}
	}
	return p.request(ctx, account, http.MethodPost, "/file/move", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, map[string]any{"filelist": clean, "to_pdir_fid": parent}, nil)
}
func (p *ProviderQuark) Delete(ctx context.Context, account *domain.DriveAccount, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("file IDs are required")
	}
	clean := make([]string, len(ids))
	for i, id := range ids {
		clean[i] = strings.TrimSpace(id)
		if clean[i] == "" {
			return fmt.Errorf("file IDs must not be empty")
		}
	}
	return p.request(ctx, account, http.MethodPost, "/file/delete", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, map[string]any{"filelist": clean, "action_type": 2}, nil)
}

func parseQuarkShare(rawURL, password string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", "", fmt.Errorf("invalid Quark share URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	id := ""
	for i := range parts {
		if (parts[i] == "s" || parts[i] == "share") && i+1 < len(parts) {
			id = parts[i+1]
			break
		}
	}
	if id == "" && len(parts) == 1 {
		id = parts[0]
	}
	if id == "" || strings.ContainsAny(id, "?#/") {
		return "", "", fmt.Errorf("cannot parse Quark share ID")
	}
	if password == "" {
		password = parsed.Query().Get("pwd")
	}
	if password == "" {
		password = parsed.Query().Get("password")
	}
	return id, strings.TrimSpace(password), nil
}

func (p *ProviderQuark) shareToken(ctx context.Context, account *domain.DriveAccount, rawURL, password string) (string, string, error) {
	shareID, password, err := parseQuarkShare(rawURL, password)
	if err != nil {
		return "", "", err
	}
	var data struct {
		Stoken string `json:"stoken"`
		Title  string `json:"title"`
	}
	err = p.request(ctx, account, http.MethodPost, "/share/sharepage/token", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, map[string]any{"pwd_id": shareID, "passcode": password}, &data)
	if err != nil {
		return "", "", err
	}
	if data.Stoken == "" {
		return "", "", fmt.Errorf("Quark share is expired or password is invalid")
	}
	return shareID, data.Stoken, nil
}

func (p *ProviderQuark) shareEntries(ctx context.Context, account *domain.DriveAccount, shareID, stoken, parent string) ([]ShareEntry, error) {
	entries := make([]ShareEntry, 0)
	seen := map[string]struct{}{}
	if parent == "" {
		parent = "0"
	}
	for page := 1; ; page++ {
		var data quarkListData
		query := url.Values{"pr": {"ucpro"}, "fr": {"pc"}, "pwd_id": {shareID}, "stoken": {stoken}, "pdir_fid": {parent}, "force": {"0"}, "_page": {strconv.Itoa(page)}, "_size": {"100"}, "_fetch_banner": {"0"}, "_fetch_share": {"0"}, "_fetch_total": {"1"}, "_sort": {"file_type:asc,updated_at:desc"}}
		if err := p.request(ctx, account, http.MethodGet, "/share/sharepage/detail", query, nil, &data); err != nil {
			return entries, err
		}
		if len(data.List) == 0 {
			break
		}
		before := len(entries)
		for _, item := range data.List {
			file, err := parseQuarkFile(item, "")
			if err != nil {
				return entries, err
			}
			if _, duplicate := seen[file.FileID]; duplicate {
				continue
			}
			seen[file.FileID] = struct{}{}
			entries = append(entries, ShareEntry{ID: file.FileID, Name: file.Name, Size: file.Size, IsDir: file.IsFolder, ParentID: file.ParentID, Revision: file.Revision})
			if len(entries) > quarkMaxEntries {
				return entries, fmt.Errorf("Quark share contains more than %d entries", quarkMaxEntries)
			}
		}
		if len(entries) == before {
			return entries, fmt.Errorf("Quark share pagination made no progress")
		}
		if len(data.List) < 100 {
			break
		}
	}
	return entries, nil
}
func (p *ProviderQuark) SnapshotShare(ctx context.Context, account *domain.DriveAccount, rawURL, password string) (ShareSnapshot, error) {
	shareID, stoken, err := p.shareToken(ctx, account, rawURL, password)
	if err != nil {
		return ShareSnapshot{}, err
	}
	entries, err := p.shareEntries(ctx, account, shareID, stoken, "0")
	if err != nil {
		return ShareSnapshot{}, err
	}
	return ShareSnapshot{Entries: entries}, nil
}
func (p *ProviderQuark) SaveShare(ctx context.Context, account *domain.DriveAccount, rawURL, password, parent string) (SavedShare, error) {
	shareID, stoken, err := p.shareToken(ctx, account, rawURL, password)
	if err != nil {
		return SavedShare{}, err
	}
	entries, err := p.shareEntries(ctx, account, shareID, stoken, "0")
	if err != nil {
		return SavedShare{}, err
	}
	if len(entries) == 0 {
		return SavedShare{}, fmt.Errorf("Quark share is empty or expired")
	}
	if parent == "" {
		parent = "0"
	}
	ids := make([]string, len(entries))
	tokens := make([]string, len(entries))
	for i := range entries {
		ids[i] = entries[i].ID
		tokens[i] = stoken
	}
	var saved struct {
		TaskID any      `json:"task_id"`
		FIDs   []string `json:"fids"`
	}
	if err := p.request(ctx, account, http.MethodPost, "/share/sharepage/save", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, map[string]any{"fid_list": ids, "fid_token_list": tokens, "to_pdir_fid": parent, "pwd_id": shareID, "stoken": stoken, "pdir_fid": "0", "scene": "link"}, &saved); err != nil {
		return SavedShare{}, err
	}
	rootIDs := append([]string(nil), saved.FIDs...)
	taskID := quarkScalar(saved.TaskID)
	if len(rootIDs) == 0 && taskID != "" {
		rootIDs, err = p.waitTask(ctx, account, taskID)
		if err != nil {
			return SavedShare{}, err
		}
	}
	if len(rootIDs) == 0 {
		return SavedShare{}, fmt.Errorf("Quark share save completed without stable root IDs")
	}
	return SavedShare{RootIDs: rootIDs, Count: len(rootIDs)}, nil
}
func (p *ProviderQuark) waitTask(ctx context.Context, account *domain.DriveAccount, taskID string) ([]string, error) {
	for attempt := 0; attempt < 120; attempt++ {
		var data struct {
			Status     any `json:"status"`
			FinishedAt any `json:"finished_at"`
			SaveAs     struct {
				TopFIDs []string `json:"save_as_top_fids"`
				FIDs    []string `json:"save_as_fids"`
			} `json:"save_as"`
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := p.request(ctx, account, http.MethodGet, "/task", url.Values{"pr": {"ucpro"}, "fr": {"pc"}, "task_id": {taskID}, "retry_index": {"0"}}, nil, &data); err != nil {
			return nil, err
		}
		status := strings.ToLower(quarkScalar(data.Status))
		if status == "2" || status == "success" || quarkScalar(data.FinishedAt) != "" {
			ids := data.SaveAs.TopFIDs
			if len(ids) == 0 {
				ids = data.SaveAs.FIDs
			}
			if len(ids) == 0 {
				return nil, fmt.Errorf("Quark task completed without saved object IDs")
			}
			return ids, nil
		}
		if status == "3" || status == "failed" || data.Error != "" {
			message := data.Error
			if message == "" {
				message = data.Message
			}
			return nil, fmt.Errorf("Quark share save failed: %s", message)
		}
		if err := p.sleep(ctx, time.Second); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("Quark share save task did not complete before timeout")
}

func (p *ProviderQuark) downloadURL(ctx context.Context, account *domain.DriveAccount, id string) (string, error) {
	var data []struct {
		DownloadURL string `json:"download_url"`
		URL         string `json:"url"`
	}
	if err := p.request(ctx, account, http.MethodPost, "/file/download", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, map[string]any{"fids": []string{id}}, &data); err != nil {
		return "", err
	}
	if len(data) != 1 {
		return "", fmt.Errorf("Quark download response did not identify one file")
	}
	signed := data[0].DownloadURL
	if signed == "" {
		signed = data[0].URL
	}
	parsed, err := url.Parse(signed)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("Quark download response contained an invalid signed URL")
	}
	return signed, nil
}
func (p *ProviderQuark) OpenDownload(ctx context.Context, account *domain.DriveAccount, id string, offset int64) (io.ReadCloser, error) {
	if strings.TrimSpace(id) == "" || offset < 0 {
		return nil, fmt.Errorf("file ID and nonnegative offset are required")
	}
	for attempt := 0; attempt < 2; attempt++ {
		signed, err := p.downloadURL(ctx, account, id)
		if err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/124 Safari/537.36")
		if offset > 0 {
			request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		response, err := p.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("open Quark download: %w", err)
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			response.Body.Close()
			continue
		}
		want := http.StatusOK
		if offset > 0 {
			want = http.StatusPartialContent
		}
		if response.StatusCode != want {
			response.Body.Close()
			return nil, &ProviderHTTPError{Operation: "Quark download", StatusCode: response.StatusCode}
		}
		return response.Body, nil
	}
	return nil, fmt.Errorf("Quark signed download URL expired after refresh")
}
