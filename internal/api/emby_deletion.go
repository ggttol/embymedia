package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/labstack/echo/v4"
)

const embyDeletionPrefix = "/internal/emby-delete"

var embyDeletionClient = &http.Client{
	Timeout: 2 * time.Minute,
	// Redirects must not send a human's Emby credentials to another origin.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

var embyAuthorizationToken = regexp.MustCompile(`(?i)(?:^|[ ,])Token\s*=\s*(?:"([^"]+)"|([^,\s]+))`)
var embyAuthorizationDeviceID = regexp.MustCompile(`(?i)(?:^|[ ,])DeviceId\s*=\s*(?:"([^"]+)"|([^,\s]+))`)

type embyDeletionRequest struct {
	path         string
	ids          []string
	confirmation bool
	query        url.Values
	authQuery    url.Values
	headers      http.Header
	body         []byte
	credential   string
	deviceID     string
}

type embyDeletionResponse struct {
	status      int
	contentType string
	body        []byte
}

func validEmbyDeletionID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, char := range id {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '-') {
			return false
		}
	}
	return true
}

func embyDeletionIDs(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "deletion requires 1 to 100 unique Emby item IDs")
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !validEmbyDeletionID(id) {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid Emby deletion item ID")
		}
		if _, exists := seen[id]; exists {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "duplicate Emby deletion item ID")
		}
		seen[id] = struct{}{}
	}
	return ids, nil
}

func embyDeletionBodyIDs(body []byte, contentType string) ([]string, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	kind, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "deletion body requires a supported Content-Type")
	}
	if kind == "application/x-www-form-urlencoded" {
		values, err := url.ParseQuery(string(body))
		if err == nil && len(values) == 1 {
			for key, values := range values {
				if strings.EqualFold(key, "Ids") && len(values) == 1 {
					return embyDeletionIDs(strings.Split(values[0], ","))
				}
			}
		}
		return nil, echo.NewHTTPError(http.StatusBadRequest, "deletion form must contain only one Ids field")
	}
	if kind != "application/json" {
		return nil, echo.NewHTTPError(http.StatusUnsupportedMediaType, "deletion body must be JSON or form data")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "deletion JSON must be an object containing Ids")
	}
	var ids []string
	if decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || !strings.EqualFold(name, "Ids") {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "deletion JSON supports only Ids")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid deletion JSON")
		}
		var commaList string
		if json.Unmarshal(raw, &commaList) == nil {
			ids = strings.Split(commaList, ",")
		} else if json.Unmarshal(raw, &ids) != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "Ids must be a comma-separated string or string array")
		}
		if ids, err = embyDeletionIDs(ids); err != nil {
			return nil, err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "duplicate or unsupported deletion JSON field")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "unexpected data after deletion JSON")
	}
	return ids, nil
}

func parseEmbyDeletion(request *http.Request) (*embyDeletionRequest, error) {
	path, found := strings.CutPrefix(request.URL.Path, embyDeletionPrefix)
	if !found {
		return nil, echo.NewHTTPError(http.StatusNotFound, "unsupported Emby deletion endpoint")
	}
	if strings.HasPrefix(path, "/emby/") {
		path = strings.TrimPrefix(path, "/emby")
	}
	path = strings.TrimSuffix(path, "/")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	target := &embyDeletionRequest{path: path, authQuery: make(url.Values), headers: make(http.Header)}
	switch {
	case request.Method == http.MethodGet && len(parts) == 3 && parts[0] == "Items" && parts[2] == "DeleteInfo":
		target.ids = []string{parts[1]}
		target.confirmation = true
	case request.Method == http.MethodDelete && len(parts) == 2 && parts[0] == "Items" && parts[1] != "Delete":
		target.ids = []string{parts[1]}
	case request.Method == http.MethodPost && len(parts) == 3 && parts[0] == "Items" && parts[2] == "Delete":
		target.ids = []string{parts[1]}
	case request.Method == http.MethodDelete && path == "/Items":
	case request.Method == http.MethodPost && path == "/Items/Delete":
	default:
		return nil, echo.NewHTTPError(http.StatusNotFound, "unsupported Emby deletion endpoint")
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid Emby deletion query")
	}
	target.query = query
	var queryIDs []string
	var credential string
	acceptCredential := func(value string) error {
		if strings.TrimSpace(value) == "" || credential != "" && credential != value {
			return echo.NewHTTPError(http.StatusUnauthorized, "missing or conflicting Emby user credentials")
		}
		credential = value
		return nil
	}
	acceptDevice := func(value string) error {
		if strings.TrimSpace(value) == "" || len(value) > 1024 || target.deviceID != "" && target.deviceID != value {
			return echo.NewHTTPError(http.StatusUnauthorized, "missing or conflicting Emby session device ID")
		}
		target.deviceID = value
		return nil
	}
	seen := make(map[string]bool, len(query))
	for key, values := range query {
		lower := strings.ToLower(key)
		if seen[lower] || len(values) != 1 {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "duplicate Emby deletion query parameter")
		}
		seen[lower] = true
		switch lower {
		case "ids":
			queryIDs, err = embyDeletionIDs(strings.Split(values[0], ","))
			if err != nil {
				return nil, err
			}
		case "api_key", "x-emby-token":
			if err := acceptCredential(values[0]); err != nil {
				return nil, err
			}
			target.authQuery[key] = values
		case "deviceid", "x-emby-device-id":
			if err := acceptDevice(values[0]); err != nil {
				return nil, err
			}
			if lower == "deviceid" {
				target.authQuery["DeviceId"] = values
			} else {
				target.authQuery["X-Emby-Device-Id"] = values
			}
		case "x-emby-client", "x-emby-device-name", "x-emby-client-version", "x-emby-language":
			if len(values[0]) > 1024 {
				return nil, echo.NewHTTPError(http.StatusBadRequest, "Emby client metadata exceeds 1024 bytes")
			}
			target.authQuery[key] = values
		default:
			return nil, echo.NewHTTPError(http.StatusBadRequest, "unsupported Emby deletion query parameter")
		}
	}
	for _, key := range []string{"X-Emby-Token", "X-MediaBrowser-Token", "Authorization", "X-Emby-Authorization", "X-Emby-Device-Id"} {
		values := request.Header.Values(key)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 {
			return nil, echo.NewHTTPError(http.StatusUnauthorized, "duplicate Emby credential header")
		}
		value := values[0]
		if key == "Authorization" || key == "X-Emby-Authorization" {
			scheme, remainder, _ := strings.Cut(value, " ")
			if strings.EqualFold(scheme, "Bearer") {
				err = acceptCredential(remainder)
			} else if strings.EqualFold(scheme, "Emby") || strings.EqualFold(scheme, "MediaBrowser") {
				matches := embyAuthorizationToken.FindAllStringSubmatch(value, -1)
				if len(matches) > 1 {
					return nil, echo.NewHTTPError(http.StatusUnauthorized, "duplicate Emby authorization token")
				}
				if len(matches) == 1 {
					err = acceptCredential(matches[0][1] + matches[0][2])
				}
				devices := embyAuthorizationDeviceID.FindAllStringSubmatch(value, -1)
				if len(devices) > 1 {
					return nil, echo.NewHTTPError(http.StatusUnauthorized, "duplicate Emby session device ID")
				}
				if len(devices) == 1 {
					if deviceErr := acceptDevice(devices[0][1] + devices[0][2]); deviceErr != nil {
						return nil, deviceErr
					}
				}
			} else {
				return nil, echo.NewHTTPError(http.StatusUnauthorized, "unsupported Emby authorization scheme")
			}
		} else if key == "X-Emby-Device-Id" {
			err = acceptDevice(value)
		} else {
			err = acceptCredential(value)
		}
		if err != nil {
			return nil, err
		}
		target.headers.Set(key, value)
	}
	if credential == "" {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "an authenticated Emby user token is required")
	}
	if target.deviceID == "" {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "an authenticated Emby session device ID is required")
	}
	target.credential = credential
	if request.Body != nil {
		target.body, err = io.ReadAll(io.LimitReader(request.Body, (64<<10)+1))
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "cannot read Emby deletion body")
		}
		if len(target.body) > 64<<10 {
			return nil, echo.NewHTTPError(http.StatusRequestEntityTooLarge, "Emby deletion body exceeds 64 KiB")
		}
	}
	if target.confirmation && len(target.body) != 0 {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "DeleteInfo does not accept a request body")
	}
	bodyIDs, err := embyDeletionBodyIDs(target.body, request.Header.Get(echo.HeaderContentType))
	if err != nil {
		return nil, err
	}
	for _, ids := range [][]string{queryIDs, bodyIDs} {
		if ids == nil {
			continue
		}
		if target.ids == nil {
			target.ids = ids
		} else if !sameEmbyDeletionIDs(target.ids, ids) {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "Emby deletion path, query and body must identify the same items")
		}
	}
	if target.ids, err = embyDeletionIDs(target.ids); err != nil {
		return nil, err
	}
	return target, nil
}

func sameEmbyDeletionIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for _, id := range left {
		if !slices.Contains(right, id) {
			return false
		}
	}
	return true
}

func (s *Server) embyDeletionURL() (*url.URL, error) {
	raw, err := s.db.GetSetting("emby_url")
	if err != nil || raw == "" {
		raw, _ = s.db.GetConfig("emby_url")
	}
	base, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" {
		return nil, echo.NewHTTPError(http.StatusServiceUnavailable, "configure a valid Emby server URL without credentials, query or fragment")
	}
	return base, nil
}

func (target *embyDeletionRequest) call(ctx context.Context, base *url.URL, method, path string, query url.Values, body []byte, contentType string) (*embyDeletionResponse, error) {
	upstream := *base
	upstream.Path = strings.TrimRight(base.Path, "/") + path
	upstream.RawPath = ""
	values := make(url.Values, len(query)+len(target.authQuery))
	for key, value := range target.authQuery {
		values[key] = value
	}
	for key, value := range query {
		if strings.EqualFold(key, "DeviceId") {
			key = "DeviceId"
		}
		values[key] = value
	}
	upstream.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, method, upstream.String(), bytes.NewReader(body))
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadGateway, "cannot construct native Emby request")
	}
	request.Header = target.headers.Clone()
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set(echo.HeaderContentType, contentType)
	}
	response, err := embyDeletionClient.Do(request)
	if err != nil {
		// URL errors can contain api_key; neither responses nor audit records include them.
		return nil, echo.NewHTTPError(http.StatusBadGateway, "native Emby request failed; inspect Emby before retrying a deletion")
	}
	defer response.Body.Close()
	result := &embyDeletionResponse{status: response.StatusCode, contentType: response.Header.Get(echo.HeaderContentType)}
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		if !result.successful() {
			result.contentType = echo.MIMEApplicationJSON
			result.body = []byte(`{"error":"native Emby rejected the request; its response body could not be read"}`)
			return result, nil
		}
		return result, echo.NewHTTPError(http.StatusBadGateway, "cannot read the native Emby response; inspect Emby before retrying a deletion")
	}
	result.body = data
	return result, nil
}

func (response *embyDeletionResponse) successful() bool {
	return response.status >= 200 && response.status < 300
}

// Auth/Keys supplies native administrator authorization and excludes application keys.
// The user comes from the native session for the original device, never a supplied UserId.
func (s *Server) authorizeEmbyDeletion(ctx context.Context, base *url.URL, target *embyDeletionRequest) (string, *embyDeletionResponse, error) {
	storedKey, err := s.db.GetSetting("emby_api_key")
	if err != nil || storedKey == "" {
		storedKey, _ = s.db.GetConfig("emby_api_key")
	}
	if storedKey != "" && target.credential == storedKey {
		return "", nil, echo.NewHTTPError(http.StatusForbidden, "application API keys cannot authorize user-confirmed Emby deletion")
	}
	for offset := 0; ; offset += 100 {
		if offset >= 10000 {
			return "", nil, echo.NewHTTPError(http.StatusBadGateway, "cannot completely inspect native Emby application keys")
		}
		response, err := target.call(ctx, base, http.MethodGet, "/Auth/Keys", url.Values{"StartIndex": {strconv.Itoa(offset)}, "Limit": {"100"}}, nil, "")
		if err != nil || !response.successful() {
			return "", response, err
		}
		var keys struct {
			Items *[]struct {
				AccessToken string `json:"AccessToken"`
			} `json:"Items"`
		}
		if json.Unmarshal(response.body, &keys) != nil || keys.Items == nil {
			return "", nil, echo.NewHTTPError(http.StatusBadGateway, "cannot inspect native Emby application keys")
		}
		for _, key := range *keys.Items {
			if key.AccessToken == "" {
				return "", nil, echo.NewHTTPError(http.StatusBadGateway, "native Emby application key identity is missing")
			}
			if key.AccessToken == target.credential {
				return "", nil, echo.NewHTTPError(http.StatusForbidden, "application API keys cannot authorize user-confirmed Emby deletion")
			}
		}
		// Native 4.9 may report TotalRecordCount=0 for a nonempty page.
		if len(*keys.Items) < 100 {
			break
		}
	}
	response, err := target.call(ctx, base, http.MethodGet, "/Sessions", url.Values{"DeviceId": {target.deviceID}}, nil, "")
	if err != nil || !response.successful() {
		return "", response, err
	}
	var sessions []struct {
		ID       string `json:"Id"`
		UserID   string `json:"UserId"`
		DeviceID string `json:"DeviceId"`
	}
	if json.Unmarshal(response.body, &sessions) != nil || len(sessions) != 1 || !validEmbyDeletionID(sessions[0].ID) || !validEmbyDeletionID(sessions[0].UserID) || sessions[0].UserID == "0" || sessions[0].DeviceID != target.deviceID {
		return "", nil, echo.NewHTTPError(http.StatusForbidden, "Emby deletion requires one authenticated native user session for this device")
	}
	userID := sessions[0].UserID
	response, err = target.call(ctx, base, http.MethodGet, "/Users/"+userID, nil, nil, "")
	if err != nil || !response.successful() {
		return "", response, err
	}
	var user struct {
		ID     string `json:"Id"`
		Policy struct {
			IsAdministrator bool  `json:"IsAdministrator"`
			IsDisabled      *bool `json:"IsDisabled"`
		} `json:"Policy"`
	}
	if json.Unmarshal(response.body, &user) != nil || user.ID != userID || !user.Policy.IsAdministrator || user.Policy.IsDisabled == nil || *user.Policy.IsDisabled {
		return "", nil, echo.NewHTTPError(http.StatusForbidden, "Emby deletion requires an enabled Emby administrator")
	}
	return userID, response, nil
}

func (s *Server) handleEmbyDeletion(c echo.Context) error {
	started := time.Now()
	stage := "request"
	userID := ""
	failure := ""
	var target *embyDeletionRequest
	var plan *service.EmbyDeletionPlan
	var native *embyDeletionResponse
	var paths []string
	nativeDeleted := false
	status := http.StatusInternalServerError
	defer func() {
		input := map[string]any{"user_id": userID}
		if target != nil {
			input["item_ids"] = target.ids
		}
		if len(paths) > 0 {
			input["native_paths"] = paths
		}
		if plan != nil {
			input["account_id"] = plan.AccountID
			input["source_paths"] = plan.SourcePaths
			input["sources"] = plan.Sources
		}
		outcome := "error"
		if status >= 200 && status < 300 {
			outcome = "success"
		} else if status == http.StatusUnauthorized || status == http.StatusForbidden {
			outcome = "denied"
		}
		s.writeAudit(&domain.AuditLog{
			Caller: "emby-user", Action: c.Request().Method + " emby-delete", Target: "Emby media deletion",
			Input: security.Summary(input, 0), Output: security.Summary(map[string]any{"stage": stage, "http_status": status, "native_deleted": nativeDeleted, "error": failure}, 4096),
			Status: outcome, LatencyMS: time.Since(started).Milliseconds(), IP: c.RealIP(), CreatedAt: time.Now(),
		})
	}()
	fail := func(err error) error {
		var httpError *echo.HTTPError
		if failure == "" {
			failure = security.TextSummary(err.Error(), 2048)
		}
		if errors.As(err, &httpError) {
			status = httpError.Code
			return c.JSON(status, map[string]any{"error": httpError.Message})
		}
		status = http.StatusInternalServerError
		return c.JSON(status, map[string]any{"error": "Emby deletion failed"})
	}
	var err error
	target, err = parseEmbyDeletion(c.Request())
	if err != nil {
		return fail(err)
	}
	base, err := s.embyDeletionURL()
	if err != nil {
		return fail(err)
	}
	if s.taskQueue == nil {
		return fail(echo.NewHTTPError(http.StatusServiceUnavailable, "Emby source deletion is unavailable"))
	}
	err = s.taskQueue.TryMediaMutation(func() error {
		ctx := c.Request().Context()
		stage = "authorization"
		userID, native, err = s.authorizeEmbyDeletion(ctx, base, target)
		if err != nil || !native.successful() {
			return err
		}
		paths = make([]string, 0, len(target.ids))
		var info map[string]json.RawMessage
		for _, id := range target.ids {
			stage = "item-authorization"
			native, err = target.call(ctx, base, http.MethodGet, "/Users/"+userID+"/Items/"+id, url.Values{"Fields": {"CanDelete"}}, nil, "")
			if err != nil || !native.successful() {
				return err
			}
			var item struct {
				ID        string `json:"Id"`
				CanDelete bool   `json:"CanDelete"`
			}
			if json.Unmarshal(native.body, &item) != nil || item.ID != id || !item.CanDelete {
				return echo.NewHTTPError(http.StatusForbidden, "Emby does not allow this user to delete every requested item")
			}
			stage = "native-preflight"
			native, err = target.call(ctx, base, http.MethodGet, "/Items/"+id+"/DeleteInfo", nil, nil, "")
			if err != nil || !native.successful() {
				return err
			}
			info = nil
			var itemPaths []string
			if json.Unmarshal(native.body, &info) != nil || json.Unmarshal(info["Paths"], &itemPaths) != nil || len(itemPaths) == 0 {
				return echo.NewHTTPError(http.StatusConflict, "Emby returned no usable deletion paths; no media was deleted")
			}
			paths = append(paths, itemPaths...)
		}
		stage = "source-preflight"
		plan, err = s.taskQueue.PrepareEmbyDeletionCtx(ctx, paths)
		if err != nil {
			failure = security.TextSummary(err.Error(), 2048)
			return echo.NewHTTPError(http.StatusConflict, "cannot safely resolve all original 115 videos for these paths; no media was deleted")
		}
		if target.confirmation {
			for _, path := range plan.SourcePaths {
				if !slices.Contains(paths, path) {
					paths = append(paths, path)
				}
			}
			info["Paths"], err = json.Marshal(paths)
			if err == nil {
				native.body, err = json.Marshal(info)
			}
			native.contentType = echo.MIMEApplicationJSON
			stage = "confirmation"
			return err
		}
		stage = "native-deletion"
		native, err = target.call(ctx, base, c.Request().Method, target.path, target.query, target.body, c.Request().Header.Get(echo.HeaderContentType))
		if native == nil || !native.successful() {
			return err
		}
		nativeDeleted = true
		stage = "source-recycling"
		// Once native deletion succeeds, a disconnected browser must not cancel recycling.
		recycleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := s.taskQueue.RecycleEmbyDeletionCtx(recycleCtx, plan); err != nil {
			failure = security.TextSummary(err.Error(), 2048)
			return echo.NewHTTPError(http.StatusBadGateway, "Emby deleted the requested items, but recycling original 115 videos failed or is incomplete. No rollback was performed; inspect the deletion audit and 115 before taking further action.")
		}
		stage = "completed"
		if err != nil {
			return echo.NewHTTPError(http.StatusBadGateway, "Emby deleted the requested items and original 115 recycling completed, but the native response body could not be read. Do not repeat the deletion.")
		}
		return nil
	})
	if errors.Is(err, service.ErrMediaMutationBusy) {
		return fail(echo.NewHTTPError(http.StatusConflict, service.ErrMediaMutationBusy.Error()))
	}
	if err != nil {
		return fail(err)
	}
	status = native.status
	return c.Blob(status, native.contentType, native.body)
}
