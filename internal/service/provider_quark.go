package service

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
	"strconv"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"golang.org/x/net/proxy"
)

const (
	quarkDefaultBaseURL = "https://drive.quark.cn/1/clouddrive"
	quarkMaxEntries     = 10_000
	quarkMaxDepth       = 20
)

type ProviderQuark struct {
	client          *http.Client
	baseURL         string
	downloadBaseURL string
	proxyURL        func() string
	sleep           func(context.Context, time.Duration) error
}

func NewProviderQuark(client *http.Client) *ProviderQuark {
	return &ProviderQuark{client: client, baseURL: quarkDefaultBaseURL, downloadBaseURL: "https://drive-pc.quark.cn/1/clouddrive", sleep: sleepContext}
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
	return p.requestAt(ctx, p.baseURL, account, method, path, query, body, target)
}

func (p *ProviderQuark) requestAt(ctx context.Context, baseURL string, account *domain.DriveAccount, method, path string, query url.Values, body any, target any) error {
	return p.requestWith(ctx, p.client, baseURL, account, method, path, query, body, target)
}

func (p *ProviderQuark) requestWith(ctx context.Context, client *http.Client, baseURL string, account *domain.DriveAccount, method, path string, query url.Values, body any, target any) error {
	if strings.TrimSpace(account.Cookie) == "" {
		return fmt.Errorf("quark account cookie is empty")
	}
	endpoint := strings.TrimRight(baseURL, "/") + path
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
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return safeProviderRequestError("quark "+strings.TrimPrefix(path, "/"), err)
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

func safeProviderRequestError(operation string, err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %v", operation, urlErr.Err)
	}
	return fmt.Errorf("%s request failed", operation)
}

func (p *ProviderQuark) CheckAccount(ctx context.Context, account *domain.DriveAccount) error {
	_, _, err := p.ListFiles(ctx, account, "0", 0, 1)
	return err
}

type quarkFile struct {
	FID        any    `json:"fid"`
	ParentFID  any    `json:"pdir_fid"`
	Name       string `json:"file_name"`
	Size       any    `json:"size"`
	FileSize   any    `json:"file_size"`
	Dir        any    `json:"dir"`
	UpdatedAt  any    `json:"updated_at"`
	Revision   any    `json:"revision"`
	SHA1       string `json:"sha1"`
	ShareToken string `json:"share_fid_token"`
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
			entries = append(entries, ShareEntry{ID: file.FileID, Name: file.Name, Size: file.Size, IsDir: file.IsFolder, ParentID: file.ParentID, Revision: file.Revision, ShareToken: item.ShareToken})
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

func (p *ProviderQuark) listDirectoryAll(ctx context.Context, account *domain.DriveAccount, parent string) ([]domain.DriveFile, error) {
	files := make([]domain.DriveFile, 0)
	for offset := 0; offset < quarkMaxEntries; offset += 200 {
		page, total, err := p.ListFiles(ctx, account, parent, offset, 200)
		if err != nil {
			return nil, err
		}
		files = append(files, page...)
		if len(page) == 0 || int64(offset+len(page)) >= total {
			return files, nil
		}
	}
	return nil, fmt.Errorf("Quark directory contains more than %d entries", quarkMaxEntries)
}

func resolveQuarkSavedRoots(entries []ShareEntry, before, after []domain.DriveFile, providerIDs []string) ([]string, error) {
	afterByID := make(map[string]domain.DriveFile, len(after))
	for _, file := range after {
		afterByID[file.FileID] = file
	}
	if len(providerIDs) == len(entries) {
		valid := true
		for index, id := range providerIDs {
			file, present := afterByID[id]
			entry := entries[index]
			if !present || file.Name != entry.Name || file.IsFolder != entry.IsDir || (!entry.IsDir && file.Size != entry.Size) {
				valid = false
				break
			}
		}
		if valid {
			return providerIDs, nil
		}
	}
	beforeIDs := make(map[string]struct{}, len(before))
	for _, file := range before {
		beforeIDs[file.FileID] = struct{}{}
	}
	resolved := make([]string, 0, len(entries))
	for _, entry := range entries {
		matches := make([]string, 0, 1)
		for _, file := range after {
			if _, existed := beforeIDs[file.FileID]; existed {
				continue
			}
			if file.Name == entry.Name && file.IsFolder == entry.IsDir && (entry.IsDir || file.Size == entry.Size) {
				matches = append(matches, file.FileID)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("Quark saved object %q could not be identified unambiguously in the target directory", entry.Name)
		}
		resolved = append(resolved, matches[0])
	}
	return resolved, nil
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
	before, err := p.listDirectoryAll(ctx, account, parent)
	if err != nil {
		return SavedShare{}, fmt.Errorf("list Quark target before share save: %w", err)
	}
	ids := make([]string, len(entries))
	tokens := make([]string, len(entries))
	for i := range entries {
		ids[i] = entries[i].ID
		tokens[i] = strings.TrimSpace(entries[i].ShareToken)
		if tokens[i] == "" {
			return SavedShare{}, fmt.Errorf("Quark share entry %s did not include a save token", entries[i].ID)
		}
	}
	var saved struct {
		TaskID any      `json:"task_id"`
		FIDs   []string `json:"fids"`
	}
	if err := p.request(ctx, account, http.MethodPost, "/share/sharepage/save", url.Values{"pr": {"ucpro"}, "fr": {"pc"}}, map[string]any{"fid_list": ids, "fid_token_list": tokens, "share_fid_token_list": tokens, "to_pdir_fid": parent, "pwd_id": shareID, "stoken": stoken, "pdir_fid": "0", "scene": "link"}, &saved); err != nil {
		return SavedShare{}, err
	}
	rootIDs := append([]string(nil), saved.FIDs...)
	if taskID := quarkScalar(saved.TaskID); len(rootIDs) == 0 && taskID != "" {
		rootIDs, err = p.waitTask(ctx, account, taskID)
		if err != nil {
			return SavedShare{}, err
		}
	}
	for attempt := 0; attempt < 30; attempt++ {
		after, listErr := p.listDirectoryAll(ctx, account, parent)
		if listErr != nil {
			return SavedShare{}, fmt.Errorf("list Quark target after share save: %w", listErr)
		}
		resolved, resolveErr := resolveQuarkSavedRoots(entries, before, after, rootIDs)
		if resolveErr == nil {
			return SavedShare{RootIDs: resolved, Count: len(resolved)}, nil
		}
		if attempt == 29 {
			return SavedShare{}, resolveErr
		}
		if err := p.sleep(ctx, time.Second); err != nil {
			return SavedShare{}, err
		}
	}
	return SavedShare{}, fmt.Errorf("Quark saved objects did not appear in the target directory")
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
	// When a proxy is configured, the download-URL request must use the same
	// egress as the CDN fetch: signed URLs are bound to the requesting IP.
	requestClient := p.client
	if p.proxyURL != nil && strings.TrimSpace(p.proxyURL()) != "" {
		proxyClient, err := p.downloadHTTPClient()
		if err != nil {
			return "", err
		}
		requestClient = proxyClient
	}
	if err := p.requestWith(ctx, requestClient, p.downloadBaseURL, account, http.MethodPost, "/file/download", url.Values{"pr": {"ucpro"}, "fr": {"pc"}, "sys": {"win32"}, "ve": {"2.5.56"}, "ut": {""}, "guid": {""}}, map[string]any{"fids": []string{id}}, &data); err != nil {
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

func (p *ProviderQuark) downloadHTTPClient() (*http.Client, error) {
	if p.proxyURL == nil {
		return &http.Client{Transport: p.client.Transport, CheckRedirect: p.client.CheckRedirect}, nil
	}
	proxySetting := strings.TrimSpace(p.proxyURL())
	if proxySetting == "" {
		return &http.Client{Transport: p.client.Transport, CheckRedirect: p.client.CheckRedirect}, nil
	}
	parsed, err := url.Parse(proxySetting)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "socks5") {
		return nil, fmt.Errorf("quark_download_proxy must be an HTTP or SOCKS5 URL")
	}
	var transport http.RoundTripper
	if parsed.Scheme == "socks5" {
		dialer, err := proxyFromURL(parsed)
		if err != nil {
			return nil, fmt.Errorf("parse Quark download SOCKS proxy: %w", err)
		}
		base := p.client.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		clone := base.(*http.Transport).Clone()
		clone.DialContext = dialer
		transport = clone
	} else {
		transport = &http.Transport{Proxy: http.ProxyURL(parsed)}
	}
	return &http.Client{Transport: transport, CheckRedirect: p.client.CheckRedirect}, nil
}

func proxyFromURL(parsed *url.URL) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	var auth *proxy.Auth
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		auth = &proxy.Auth{User: parsed.User.Username(), Password: password}
	}
	dialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
	if err != nil {
		return nil, err
	}
	contextDialer := dialer.(proxy.ContextDialer)
	return contextDialer.DialContext, nil
}

func (p *ProviderQuark) OpenDownload(ctx context.Context, account *domain.DriveAccount, id string, offset int64) (io.ReadCloser, error) {
	if strings.TrimSpace(id) == "" || offset < 0 {
		return nil, fmt.Errorf("file ID and nonnegative offset are required")
	}
	downloadClient, err := p.downloadHTTPClient()
	if err != nil {
		return nil, err
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
		response, err := downloadClient.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, safeProviderRequestError("open Quark download", err)
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
