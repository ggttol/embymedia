package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/labstack/echo/v4"
)

const deletionHumanToken = "human-session-secret"
const deletionDeviceID = "browser-device"
const deletionApplicationKey = "other-application-key-secret"

type deletionTestTransport func(*http.Request) (*http.Response, error)

func (fn deletionTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type deletionGatewayFixture struct {
	db                         *storage.DB
	echo                       *echo.Echo
	queue                      *service.TaskQueueService
	mu                         sync.Mutex
	mediaRoot                  string
	strmRoot                   string
	paths                      map[string][]string
	cloud                      map[string]bool
	denied                     map[string]bool
	missingGrant               bool
	nonAdmin                   bool
	disabled                   bool
	missingPolicy              bool
	missingSession             bool
	duplicateSession           bool
	wrongSessionDevice         bool
	applicationKeyOnSecondPage bool
	nativeStatus               int
	recycleFailure             bool
	events                     []string
	nativeDeleted              []string
	recycled                   []string
}

func newDeletionGatewayFixture(t *testing.T) *deletionGatewayFixture {
	t.Helper()
	root := t.TempDir()
	fixture := &deletionGatewayFixture{
		mediaRoot: filepath.Join(root, "media"), strmRoot: filepath.Join(root, "strm-v2"),
		paths: map[string][]string{"100": {"/strm-v2/TV/100.strm"}, "200": {"/strm-v2/TV/200.strm"}},
		cloud: map[string]bool{"100": true, "200": true}, denied: make(map[string]bool), nativeStatus: http.StatusNoContent,
	}
	for _, root := range []string{fixture.mediaRoot, fixture.strmRoot} {
		if err := os.MkdirAll(filepath.Join(root, "TV"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"100", "200"} {
		if err := os.WriteFile(filepath.Join(fixture.mediaRoot, "TV", id+".mkv"), []byte("episode"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.strmRoot, "TV", id+".strm"), []byte("/media/TV/"+id+".mkv\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		respond := func(value any) { _ = json.NewEncoder(w).Encode(value) }
		fixture.events = append(fixture.events, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/Library/VirtualFolders":
			respond([]map[string]any{{"Name": "TV", "ItemId": "library", "CollectionType": "tvshows", "Locations": []string{"/strm-v2/TV"}}})
			return
		case "/files":
			entries := []map[string]any{}
			if r.URL.Query().Get("cid") == "library-cid" && r.URL.Query().Get("offset") == "0" {
				for _, id := range []string{"100", "200"} {
					if fixture.cloud[id] {
						entries = append(entries, map[string]any{"fid": id, "cid": "library-cid", "n": id + ".mkv", "s": 7, "sha": ""})
					}
				}
			}
			respond(map[string]any{"state": true, "count": len(entries), "data": entries})
			return
		case "/rb/delete":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			id := r.Form.Get("fid[0]")
			fixture.recycled = append(fixture.recycled, id)
			if !slices.Contains(fixture.nativeDeleted, id) {
				t.Errorf("recycled source %s before its native deletion", id)
			}
			if fixture.recycleFailure {
				respond(map[string]any{"state": false, "error": "provider recycle denied"})
				return
			}
			if !fixture.cloud[id] {
				t.Errorf("recycled unknown source ID %s", id)
			}
			delete(fixture.cloud, id)
			if err := os.Remove(filepath.Join(fixture.mediaRoot, "TV", id+".mkv")); err != nil {
				t.Error(err)
			}
			respond(map[string]any{"state": true})
			return
		}
		authorized := r.Header.Get("X-Emby-Token") == deletionHumanToken || r.Header.Get("X-Emby-Token") == deletionApplicationKey || r.URL.Query().Get("api_key") == deletionHumanToken || r.URL.Query().Get("X-Emby-Token") == deletionHumanToken || strings.Contains(r.Header.Get("X-Emby-Authorization"), `Token="`+deletionHumanToken+`"`) || r.Header.Get("Authorization") == "Bearer "+deletionHumanToken
		if !authorized {
			w.WriteHeader(http.StatusUnauthorized)
			respond(map[string]any{"error": "native user authentication failed"})
			return
		}
		if r.Header.Get("Remote-User") != "" || r.Header.Get("Cookie") != "" {
			t.Error("gateway forwarded non-Emby proxy credentials")
		}
		if r.URL.Path == "/Auth/Keys" {
			if fixture.nonAdmin {
				w.WriteHeader(http.StatusForbidden)
				respond(map[string]any{"error": "native administrator authentication required"})
				return
			}
			keys := []map[string]any{{"AccessToken": deletionApplicationKey, "UserId": 0, "IsActive": true}}
			if fixture.applicationKeyOnSecondPage && r.URL.Query().Get("StartIndex") == "0" {
				keys = make([]map[string]any, 100)
				for index := range keys {
					keys[index] = map[string]any{"AccessToken": fmt.Sprintf("unrelated-key-%d", index), "UserId": 0, "IsActive": true}
				}
			}
			respond(map[string]any{"Items": keys, "TotalRecordCount": 0})
			return
		}
		if r.URL.Path == "/Sessions" {
			if r.URL.Query().Get("DeviceId") != deletionDeviceID {
				t.Error("session query did not use the original native device")
			}
			sessions := []map[string]any{{"Id": "native-session", "UserId": "admin", "DeviceId": deletionDeviceID}}
			if fixture.wrongSessionDevice {
				sessions[0]["DeviceId"] = "different-device"
			}
			if fixture.missingSession {
				sessions = nil
			}
			if fixture.duplicateSession {
				sessions = append(sessions, map[string]any{"Id": "second-session", "UserId": "another-user", "DeviceId": deletionDeviceID})
			}
			respond(sessions)
			return
		}
		if r.URL.Path == "/Users/admin" {
			policy := map[string]any{"IsAdministrator": !fixture.nonAdmin, "IsDisabled": fixture.disabled}
			if fixture.missingPolicy {
				delete(policy, "IsDisabled")
			}
			respond(map[string]any{"Id": "admin", "Policy": policy})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/Users/admin/Items/") {
			id := strings.TrimPrefix(r.URL.Path, "/Users/admin/Items/")
			if r.URL.Query().Get("Fields") != "CanDelete" {
				t.Error("native item query omitted CanDelete")
			}
			item := map[string]any{"Id": id}
			if !fixture.missingGrant {
				item["CanDelete"] = !fixture.denied[id]
			}
			respond(item)
			return
		}
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/DeleteInfo") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/Items/"), "/DeleteInfo")
			respond(map[string]any{"Paths": fixture.paths[id], "RequiresRefresh": true})
			return
		}
		if r.Method != http.MethodDelete && r.Method != http.MethodPost {
			t.Errorf("unexpected native request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if fixture.nativeStatus != http.StatusNoContent {
			w.WriteHeader(fixture.nativeStatus)
			respond(map[string]any{"error": "native filesystem deletion failed"})
			return
		}
		var ids []string
		if raw := r.URL.Query().Get("Ids"); raw != "" {
			ids = strings.Split(raw, ",")
		} else if r.URL.Path != "/Items/Delete" && r.URL.Path != "/Items" {
			ids = []string{strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/Items/"), "/Delete")}
		} else {
			t.Errorf("fixture requires native query or path IDs: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, id := range ids {
			fixture.nativeDeleted = append(fixture.nativeDeleted, id)
			if err := os.Remove(filepath.Join(fixture.strmRoot, "TV", id+".strm")); err != nil {
				t.Error(err)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(provider.Close)
	endpoint, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	// DriveService uses the default transport; these non-parallel tests redirect only
	// 115 requests, leaving native Emby requests on their real httptest connection.
	transport := http.DefaultTransport
	http.DefaultTransport = deletionTestTransport(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Hostname(), ".115.com") {
			request = request.Clone(request.Context())
			request.URL.Scheme, request.URL.Host = endpoint.Scheme, endpoint.Host
		}
		return transport.RoundTrip(request)
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	fixture.db = db
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetSettings(map[string]string{
		"emby_url": provider.URL, "emby_api_key": "stored-server-secret",
		"media_root": fixture.mediaRoot, "strm_root": fixture.strmRoot, "emby_media_prefix": "/media", "c115_cid_map": `{"TV":"library-cid"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAccount(&domain.DriveAccount{ID: "account", Name: "Primary", Type: "115", Cookie: "UID=provider-cookie-secret", IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	drive := service.NewDriveService(db, provider.URL, "")
	emby := service.NewEmbyService(db)
	fixture.queue = service.NewTaskQueueService(db, drive, emby)
	fixture.echo = echo.New()
	server := NewServer(fixture.echo, db, drive, emby, service.NewCloudDriveService(db), fixture.queue, service.NewCronManager(db, fixture.queue), service.NewSettingsService(db), security.NewAgentAuthorizer(db))
	t.Cleanup(server.Close)
	return fixture
}

func (fixture *deletionGatewayFixture) request(method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, embyDeletionPrefix+path, strings.NewReader(body))
	request.Header.Set("X-Emby-Token", deletionHumanToken)
	request.Header.Set("X-Emby-Authorization", `Emby Client="Web", DeviceId="`+deletionDeviceID+`"`)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	fixture.echo.ServeHTTP(response, request)
	return response
}

func (fixture *deletionGatewayFixture) assertNoDeletion(t *testing.T) {
	t.Helper()
	if len(fixture.nativeDeleted) != 0 || len(fixture.recycled) != 0 || len(fixture.cloud) != 2 {
		t.Fatalf("rejected request mutated media: native=%v recycled=%v cloud=%v", fixture.nativeDeleted, fixture.recycled, fixture.cloud)
	}
	for _, id := range []string{"100", "200"} {
		if _, err := os.Stat(filepath.Join(fixture.strmRoot, "TV", id+".strm")); err != nil {
			t.Fatalf("rejected request lost native STRM %s: %v", id, err)
		}
	}
}

func TestEmbyDeletionRequiresNativeEnabledAdministratorAndCanDelete(t *testing.T) {
	for _, name := range []string{"no-token", "no-device", "proxy-trust", "agent-token", "stored-api-key", "other-api-key", "paginated-api-key", "non-admin", "disabled", "missing-disabled-policy", "missing-session", "duplicate-session", "wrong-session-device", "no-delete-grant", "missing-delete-grant"} {
		t.Run(name, func(t *testing.T) {
			fixture := newDeletionGatewayFixture(t)
			request := httptest.NewRequest(http.MethodDelete, embyDeletionPrefix+"/Items/100", nil)
			request.Header.Set("X-Emby-Token", deletionHumanToken)
			request.Header.Set("X-Emby-Authorization", `Emby Client="Web", DeviceId="`+deletionDeviceID+`"`)
			want := http.StatusForbidden
			switch name {
			case "no-device":
				request.Header.Del("X-Emby-Authorization")
				want = http.StatusUnauthorized
			case "stored-api-key":
				request.Header.Set("X-Emby-Token", "stored-server-secret")
			case "other-api-key", "paginated-api-key":
				request.Header.Set("X-Emby-Token", deletionApplicationKey)
				fixture.applicationKeyOnSecondPage = name == "paginated-api-key"
			case "missing-session":
				fixture.missingSession = true
			case "duplicate-session":
				fixture.duplicateSession = true
			case "wrong-session-device":
				fixture.wrongSessionDevice = true
			case "no-token", "proxy-trust":
				request.Header.Del("X-Emby-Token")
				if name == "proxy-trust" {
					request.Header.Set("Remote-User", "admin")
				}
				want = http.StatusUnauthorized
			case "agent-token":
				request.Header.Del("X-Emby-Token")
				request.Header.Set("Authorization", "Bearer agent-secret")
				want = http.StatusUnauthorized
			case "non-admin":
				fixture.nonAdmin = true
			case "disabled":
				fixture.disabled = true
			case "missing-disabled-policy":
				fixture.missingPolicy = true
			case "no-delete-grant":
				fixture.denied["100"] = true
			case "missing-delete-grant":
				fixture.missingGrant = true
			}
			response := httptest.NewRecorder()
			fixture.echo.ServeHTTP(response, request)
			if response.Code != want {
				t.Fatalf("authorization returned %d, want %d: %s", response.Code, want, response.Body.String())
			}
			fixture.assertNoDeletion(t)
			if slices.Contains(fixture.events, "GET /files") {
				t.Fatal("unauthorized deletion reached source planner")
			}
		})
	}
}

func TestEmbyDeleteInfoIncludesOriginalPathsWithoutChangingNativeFields(t *testing.T) {
	fixture := newDeletionGatewayFixture(t)
	response := fixture.request(http.MethodGet, "/emby/Items/100/DeleteInfo/", "")
	var info struct {
		Paths           []string `json:"Paths"`
		RequiresRefresh bool     `json:"RequiresRefresh"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &info) != nil || !info.RequiresRefresh || !reflect.DeepEqual(info.Paths, []string{"/strm-v2/TV/100.strm", "/media/TV/100.mkv"}) {
		t.Fatalf("confirmation omitted original or native fields: %d %s", response.Code, response.Body.String())
	}
	fixture.assertNoDeletion(t)
}

func TestEmbyDeletionNativeFailureDoesNotRecycleOriginal(t *testing.T) {
	fixture := newDeletionGatewayFixture(t)
	fixture.nativeStatus = http.StatusForbidden
	response := fixture.request(http.MethodPost, "/Items/Delete?Ids=100", "")
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "native filesystem deletion failed") {
		t.Fatalf("native failure was not preserved: %d %s", response.Code, response.Body.String())
	}
	if !slices.Contains(fixture.events, "GET /files") || !slices.Contains(fixture.events, "POST /Items/Delete") {
		t.Fatalf("native rejection was not reached after source preflight: %v", fixture.events)
	}
	fixture.assertNoDeletion(t)
}

func TestEmbyDeletionPreflightsEntireBatchBeforeMutation(t *testing.T) {
	for _, name := range []string{"unauthorized-item", "unsupported-source", "library-root"} {
		t.Run(name, func(t *testing.T) {
			fixture := newDeletionGatewayFixture(t)
			want := http.StatusConflict
			switch name {
			case "unauthorized-item":
				fixture.denied["200"] = true
				want = http.StatusForbidden
			case "unsupported-source":
				fixture.paths["200"] = []string{"/outside/200.mkv"}
			case "library-root":
				fixture.paths["200"] = []string{"/strm-v2/TV"}
			}
			response := fixture.request(http.MethodPost, "/Items/Delete?Ids=100,200", "")
			if response.Code != want {
				t.Fatalf("unsafe batch returned %d, want %d: %s", response.Code, want, response.Body.String())
			}
			fixture.assertNoDeletion(t)
		})
	}
}

func TestEmbyDeletionReportsPartialNativeSuccessAndAuditsExactSources(t *testing.T) {
	fixture := newDeletionGatewayFixture(t)
	fixture.recycleFailure = true
	response := fixture.request(http.MethodPost, "/Items/Delete?Ids=100&api_key="+deletionHumanToken, "")
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "Emby deleted") {
		t.Fatalf("partial deletion was not identified: %d %s", response.Code, response.Body.String())
	}
	if !reflect.DeepEqual(fixture.nativeDeleted, []string{"100"}) || !reflect.DeepEqual(fixture.recycled, []string{"100"}) || !fixture.cloud["100"] {
		t.Fatalf("unexpected partial outcome: native=%v recycled=%v cloud=%v", fixture.nativeDeleted, fixture.recycled, fixture.cloud)
	}
	if _, err := os.Stat(filepath.Join(fixture.strmRoot, "TV", "100.strm")); !os.IsNotExist(err) {
		t.Fatalf("native deletion was not performed: %v", err)
	}
	logs, err := fixture.db.ListAuditLogs(10)
	if err != nil || len(logs) != 1 {
		t.Fatalf("deletion audit missing: %+v %v", logs, err)
	}
	if logs[0].Status != "error" || !strings.Contains(logs[0].Output, `"native_deleted":true`) || !strings.Contains(logs[0].Input, "library-cid") || !strings.Contains(logs[0].Input, "/media/TV/100.mkv") {
		t.Fatalf("audit omitted partial outcome or source coordinates: %+v", logs[0])
	}
	encoded, _ := json.Marshal(logs)
	for _, secret := range []string{deletionHumanToken, deletionApplicationKey, "stored-server-secret", "provider-cookie-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("deletion audit contains an authentication secret")
		}
	}
}

func TestEmbyDeletionSupportsNativeMutationEndpoints(t *testing.T) {
	cases := []struct {
		method string
		path   string
		body   string
		ids    []string
	}{
		{http.MethodDelete, "/Items/100", "", []string{"100"}},
		{http.MethodDelete, "/emby/Items/100/", "", []string{"100"}},
		{http.MethodDelete, "/Items?Ids=100,200", "", []string{"100", "200"}},
		{http.MethodPost, "/emby/Items/Delete?Ids=100,200", `{"Ids":["200","100"]}`, []string{"100", "200"}},
		{http.MethodPost, "/emby/Items/100/Delete/", "", []string{"100"}},
	}
	for _, test := range cases {
		t.Run(test.method+test.path, func(t *testing.T) {
			fixture := newDeletionGatewayFixture(t)
			response := fixture.request(test.method, test.path, test.body)
			if response.Code != http.StatusNoContent || !reflect.DeepEqual(fixture.nativeDeleted, test.ids) || !reflect.DeepEqual(fixture.recycled, test.ids) {
				t.Fatalf("native route did not delete exact originals: response=%d %s native=%v recycled=%v", response.Code, response.Body.String(), fixture.nativeDeleted, fixture.recycled)
			}
			for _, id := range test.ids {
				if _, err := os.Stat(filepath.Join(fixture.mediaRoot, "TV", id+".mkv")); !os.IsNotExist(err) {
					t.Fatalf("original video %s remains: %v", id, err)
				}
			}
		})
	}
}

func TestEmbyDeletionRejectsAmbiguousIdentityAndUnsupportedRequests(t *testing.T) {
	cases := []struct {
		path string
		body string
		code int
	}{
		{"/Items/Delete?Ids=100&ids=200", "", http.StatusBadRequest},
		{"/Items/Delete?Ids=100&Ids=100", "", http.StatusBadRequest},
		{"/Items/Delete?Ids=100,100", "", http.StatusBadRequest},
		{"/Items/Delete?Ids=", "", http.StatusBadRequest},
		{"/Items/Delete?Ids=100&ItemId=200", "", http.StatusBadRequest},
		{"/Items/Delete?Ids=100&url=http://attacker.invalid", "", http.StatusBadRequest},
		{"/Items/Delete?Ids=100", `{"Ids":"200"}`, http.StatusBadRequest},
		{"/Items/Delete?Ids=100", `{"Ids":"100","Ids":"200"}`, http.StatusBadRequest},
		{"/Items/Delete?Ids=100", `{"Ids":"100","ItemId":"200"}`, http.StatusBadRequest},
		{"/Items/100/Delete?Ids=200", "", http.StatusBadRequest},
		{"/Items/100%2f200/Delete", "", http.StatusNotFound},
		{"/embyItems/100/Delete", "", http.StatusNotFound},
	}
	tooMany := make([]string, 101)
	for index := range tooMany {
		tooMany[index] = fmt.Sprint(index)
	}
	cases = append(cases, struct {
		path, body string
		code       int
	}{"/Items/Delete?Ids=" + strings.Join(tooMany, ","), "", http.StatusBadRequest})
	for _, test := range cases {
		t.Run(test.path+test.body, func(t *testing.T) {
			fixture := newDeletionGatewayFixture(t)
			response := fixture.request(http.MethodPost, test.path, test.body)
			if response.Code != test.code || len(fixture.events) != 0 {
				t.Fatalf("ambiguous request reached native Emby: %d %s events=%v", response.Code, response.Body.String(), fixture.events)
			}
			fixture.assertNoDeletion(t)
		})
	}
}

func TestEmbyDeletionBusyDoesNotCallNativeEmby(t *testing.T) {
	fixture := newDeletionGatewayFixture(t)
	if err := fixture.queue.TryMediaMutation(func() error {
		response := fixture.request(http.MethodDelete, "/Items/100", "")
		if response.Code != http.StatusConflict || len(fixture.events) != 0 {
			t.Fatalf("busy deletion reached native Emby: %d %s events=%v", response.Code, response.Body.String(), fixture.events)
		}
		fixture.assertNoDeletion(t)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEmbyDeletionPreservesNativeUserCredentialForms(t *testing.T) {
	for _, kind := range []string{"query", "web-query", "authorization", "emby-authorization"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newDeletionGatewayFixture(t)
			path := embyDeletionPrefix + "/Items/100/DeleteInfo"
			if kind == "query" {
				path += "?api_key=" + deletionHumanToken
			}
			if kind == "web-query" {
				path += "?X-Emby-Client=Emby+Web&X-Emby-Device-Name=Chrome&X-Emby-Device-Id=" + deletionDeviceID + "&X-Emby-Client-Version=4.9.5.0&X-Emby-Token=" + deletionHumanToken + "&X-Emby-Language=zh-cn"
			}
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("X-Emby-Device-Id", deletionDeviceID)
			switch kind {
			case "web-query":
				request.Header.Del("X-Emby-Device-Id")
			case "authorization":
				request.Header.Set("Authorization", "Bearer "+deletionHumanToken)
			case "emby-authorization":
				request.Header.Set("X-Emby-Authorization", `Emby Client="Web", Device="Browser", DeviceId="`+deletionDeviceID+`", Token="`+deletionHumanToken+`"`)
			}
			response := httptest.NewRecorder()
			fixture.echo.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "/media/TV/100.mkv") {
				t.Fatalf("native credential did not authorize confirmation: %d %s", response.Code, response.Body.String())
			}
			fixture.assertNoDeletion(t)
		})
	}
}

func TestEmbyDeletionNeverFollowsCredentialRedirect(t *testing.T) {
	fixture := newDeletionGatewayFixture(t)
	leaked := make(chan struct{}, 1)
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(attacker.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	if err := fixture.db.SetSetting("emby_url", redirect.URL); err != nil {
		t.Fatal(err)
	}
	response := fixture.request(http.MethodDelete, "/Items/100?api_key="+deletionHumanToken, "")
	if response.Code != http.StatusFound {
		t.Fatalf("native redirect status was not preserved: %d %s", response.Code, response.Body.String())
	}
	select {
	case <-leaked:
		t.Fatal("gateway followed a redirect with user credentials")
	default:
	}
	fixture.assertNoDeletion(t)
}

func TestEmbyDeletionRejectsStoredURLWithEmbeddedCredentials(t *testing.T) {
	fixture := newDeletionGatewayFixture(t)
	for _, raw := range []string{"http://admin:stored-secret@127.0.0.1", "http://127.0.0.1?api_key=stored-secret", "file:///tmp/emby"} {
		if err := fixture.db.SetSetting("emby_url", raw); err != nil {
			t.Fatal(err)
		}
		response := fixture.request(http.MethodDelete, "/Items/100", "")
		if response.Code != http.StatusServiceUnavailable || len(fixture.events) != 0 {
			t.Fatalf("invalid stored URL received human credentials: %d %s events=%v", response.Code, response.Body.String(), fixture.events)
		}
	}
	fixture.assertNoDeletion(t)
}

func TestEmbyDeletionUsesNativeSessionUserInsteadOfClaimedUser(t *testing.T) {
	fixture := newDeletionGatewayFixture(t)
	request := httptest.NewRequest(http.MethodGet, embyDeletionPrefix+"/Items/100/DeleteInfo?deviceid="+deletionDeviceID, nil)
	request.Header.Set("X-Emby-Token", deletionHumanToken)
	request.Header.Set("X-Emby-Authorization", `Emby Client="Web", DeviceId="`+deletionDeviceID+`", UserId="forged-user"`)
	response := httptest.NewRecorder()
	fixture.echo.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "/media/TV/100.mkv") {
		t.Fatalf("native session identity did not authorize confirmation: %d %s", response.Code, response.Body.String())
	}
	if !slices.Contains(fixture.events, "GET /Users/admin") || slices.Contains(fixture.events, "GET /Users/forged-user") {
		t.Fatalf("gateway trusted caller-supplied UserId: %v", fixture.events)
	}
	fixture.assertNoDeletion(t)
}
