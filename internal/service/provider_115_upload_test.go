package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func uploadFixtureSource(t *testing.T, content string) UploadSource {
	t.Helper()
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	full := sha1.Sum([]byte(content))
	prefix := sha1.Sum([]byte(content))
	return UploadSource{Path: path, Name: "movie.mkv", Size: int64(len(content)), SHA1: strings.ToUpper(hex.EncodeToString(full[:])), PreSHA1: strings.ToUpper(hex.EncodeToString(prefix[:]))}
}

func uploadFixtureService(t *testing.T, transport roundTripFunc) (*Provider115, *domain.DriveAccount) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	account := &domain.DriveAccount{ID: "account", Type: "115", Name: "Primary", Cookie: "cookie", Token: "access-token", IsDefault: true, Status: "active"}
	if err := db.SaveAccount(account); err != nil {
		t.Fatal(err)
	}
	service := NewDriveService(db, "", "")
	service.client.Transport = transport
	return service.providers["115"].(*Provider115), account
}

func TestProvider115RapidUploadVerifiesDestination(t *testing.T) {
	source := uploadFixtureSource(t, "content")
	listCalls := 0
	provider, account := uploadFixtureService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := ""
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		switch {
		case request.URL.Host == "webapi.115.com" && request.URL.Path == "/files":
			listCalls++
			if listCalls == 1 {
				body = `{"state":true,"count":0,"data":[]}`
			} else {
				body = `{"state":true,"count":1,"data":[{"fid":"destination","cid":"0","n":"movie.mkv","s":"7","sha":"` + source.SHA1 + `"}]}`
			}
		case request.URL.Host == "proapi.115.com" && request.URL.Path == "/open/upload/init":
			if request.Header.Get("Authorization") != "Bearer access-token" {
				t.Fatalf("missing SDK authorization")
			}
			body = `{"state":true,"data":{"status":2,"file_id":"` + source.SHA1 + `"}}`
		default:
			t.Fatalf("unexpected upload request: %s", request.URL.String())
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	result, err := provider.UploadFile(context.Background(), account, "0", source)
	if err != nil || !result.Rapid || result.FileID != "destination" || result.Transferred != 0 {
		t.Fatalf("rapid upload: %+v err=%v", result, err)
	}
}

func TestProvider115MultipartUploadAndConflict(t *testing.T) {
	source := uploadFixtureSource(t, "multipart-content")
	listCalls := 0
	partSaved := false
	provider, account := uploadFixtureService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		body := ""
		contentType := "application/json"
		switch {
		case request.URL.Host == "webapi.115.com" && request.URL.Path == "/files":
			listCalls++
			if listCalls == 1 {
				body = `{"state":true,"count":0,"data":[]}`
			} else {
				body = `{"state":true,"count":1,"data":[{"fid":"destination","cid":"0","n":"movie.mkv","s":"17","sha":"` + source.SHA1 + `"}]}`
			}
		case request.URL.Host == "proapi.115.com" && request.URL.Path == "/open/upload/init":
			body = `{"state":true,"data":{"status":1,"bucket":"bucket","object":"object","callback":{"callback":"callback-secret","callback_var":"callback-var-secret"}}}`
		case request.URL.Host == "proapi.115.com" && request.URL.Path == "/open/upload/get_token":
			body = `{"state":true,"data":{"endpoint":"https://oss.test","AccessKeyId":"id","AccessKeySecret":"secret","SecurityToken":"security"}}`
		case request.Method == http.MethodPost && request.URL.Query().Has("uploads"):
			contentType = "application/xml"
			body = `<InitiateMultipartUploadResult><Bucket>bucket</Bucket><Key>object</Key><UploadId>upload</UploadId></InitiateMultipartUploadResult>`
		case request.Method == http.MethodGet && request.URL.Query().Get("uploadId") == "upload":
			contentType = "application/xml"
			body = `<ListPartsResult><Bucket>bucket</Bucket><Key>object</Key><UploadId>upload</UploadId><IsTruncated>false</IsTruncated></ListPartsResult>`
		case request.Method == http.MethodPut && request.URL.Query().Get("partNumber") == "1":
			partSaved = true
			header.Set("ETag", `"etag"`)
		case request.Method == http.MethodPost && request.URL.Query().Get("uploadId") == "upload":
			if !partSaved {
				t.Fatal("multipart completed before part upload")
			}
			if request.Header.Get("X-Oss-Callback") != "callback-secret" {
				t.Fatal("callback omitted")
			}
			contentType = "application/xml"
			body = `<CompleteMultipartUploadResult><Bucket>bucket</Bucket><Key>object</Key><ETag>etag</ETag></CompleteMultipartUploadResult>`
		default:
			t.Fatalf("unexpected multipart request: %s %s", request.Method, request.URL.String())
		}
		header.Set("Content-Type", contentType)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	parts := 0
	source.OnPart = func(number int, etag string, size int64) error { parts++; return nil }
	result, err := provider.UploadFile(context.Background(), account, "0", source)
	if err != nil || result.Rapid || result.Transferred != source.Size || result.FileID != "destination" || parts != 1 {
		t.Fatalf("multipart: %+v parts=%d err=%v", result, parts, err)
	}
}

func TestProvider115RefusesSettledSameNameDifferentBytes(t *testing.T) {
	previous := destinationVerifyBaseTimeout
	destinationVerifyBaseTimeout = 50 * time.Millisecond
	defer func() { destinationVerifyBaseTimeout = previous }()
	source := uploadFixtureSource(t, "content")
	uploaded := false
	provider, account := uploadFixtureService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/open/upload/init" {
			uploaded = true
		}
		body := `{"state":true,"count":1,"data":[{"fid":"existing","cid":"0","n":"movie.mkv","s":"7","sha":"0000000000000000000000000000000000000000"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	if _, err := provider.UploadFile(context.Background(), account, "0", source); err == nil || !strings.Contains(err.Error(), "identity verification failed for movie.mkv") {
		t.Fatalf("settled conflict result: %v", err)
	}
	if uploaded {
		t.Fatal("a conflicting same-name object was overwritten")
	}
}

func TestProvider115WaitsWhileCloudDriveUploadsInBackground(t *testing.T) {
	previousBase, previousCeiling := destinationVerifyBaseTimeout, destinationVerifyCeiling
	destinationVerifyBaseTimeout = 150 * time.Millisecond
	destinationVerifyCeiling = 30 * time.Second
	defer func() {
		destinationVerifyBaseTimeout, destinationVerifyCeiling = previousBase, previousCeiling
	}()
	source := uploadFixtureSource(t, "content")
	started := time.Now()
	provider, account := uploadFixtureService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		// The upload runs in the background for a fixed window, so both the exact
		// lookup and the placeholder probe observe the same state per poll.
		body := `{"state":true,"count":1,"data":[{"fid":"partial","cid":"0","n":"movie.mkv**..uploading","s":"0"}]}`
		if time.Since(started) > 8*time.Second {
			body = `{"state":true,"count":1,"data":[{"fid":"destination","cid":"0","n":"movie.mkv","s":"7","sha":"` + source.SHA1 + `"}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	result, err := provider.waitForVerifiedDestination(context.Background(), account, "0", source, false, source.Size)
	if err != nil || result.FileID != "destination" {
		t.Fatalf("background upload was abandoned: %+v err=%v", result, err)
	}
	if elapsed := time.Since(started); elapsed < 8*time.Second {
		t.Fatalf("background upload returned before the placeholder cleared: %s", elapsed)
	}
}

func TestProvider115GivesUpWhenNoUploadIsInFlight(t *testing.T) {
	previousBase, previousCeiling := destinationVerifyBaseTimeout, destinationVerifyCeiling
	destinationVerifyBaseTimeout = 50 * time.Millisecond
	destinationVerifyCeiling = 500 * time.Millisecond
	defer func() {
		destinationVerifyBaseTimeout, destinationVerifyCeiling = previousBase, previousCeiling
	}()
	source := uploadFixtureSource(t, "content")
	provider, account := uploadFixtureService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"state":true,"count":0,"data":[]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
	started := time.Now()
	if _, err := provider.waitForVerifiedDestination(context.Background(), account, "0", source, false, source.Size); err == nil || !strings.Contains(err.Error(), "did not finish verifying") {
		t.Fatalf("absent destination result: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("absent destination waited %s past its idle timeout", elapsed)
	}
}

func TestProvider115RejectsUnverifiableSourceBeforeUpload(t *testing.T) {
	source := uploadFixtureSource(t, "content")
	source.SHA1 = ""
	provider, account := uploadFixtureService(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unverifiable source reached the provider: %s", request.URL.Path)
		return nil, nil
	}))
	if _, err := provider.UploadFile(context.Background(), account, "0", source); err == nil || !strings.Contains(err.Error(), "SHA-1") {
		t.Fatalf("unverifiable source result: %v", err)
	}
}
