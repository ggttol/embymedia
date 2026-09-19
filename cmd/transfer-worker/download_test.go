package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/service"
)

type downloadTransport func(*http.Request) (*http.Response, error)

func (f downloadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWorkerRefreshesExpiredDownloadCredentialAcrossRanges(t *testing.T) {
	var refreshes atomic.Int32
	client := &http.Client{Transport: downloadTransport(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		status, body := http.StatusOK, ""
		switch r.URL.Path {
		case "/1/clouddrive/file/download":
			body = `{"status":200,"data":[{"download_url":"https://cdn.invalid/file"}]}`
		case "/1/clouddrive/file/sort":
			if strings.Contains(r.Header.Get("Cookie"), "__puus=") {
				return nil, fmt.Errorf("stale credential used for refresh")
			}
			refreshes.Add(1)
			header.Set("Set-Cookie", "__puus=fresh; Path=/")
			body = `{"status":200,"data":{"list":[]}}`
		case "/file":
			if !strings.Contains(r.Header.Get("Cookie"), "__puus=fresh") {
				status = http.StatusPreconditionFailed
			} else {
				status, body = http.StatusPartialContent, "data"
				header.Set("Content-Range", "bytes 0-3/4")
			}
		default:
			return nil, fmt.Errorf("unexpected provider request")
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})}
	opener := quarkRangeDownloader(service.NewProviderQuark(client), domain.DriveAccount{ID: "account", Cookie: "sid=stable; __puus=expired"}, "episode-18")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			body, err := opener(context.Background(), 0, 4)
			if err != nil {
				t.Error(err)
				return
			}
			defer body.Close()
			content, err := io.ReadAll(body)
			if err != nil || string(content) != "data" {
				t.Errorf("range failed: %q %v", content, err)
			}
		})
	}
	wg.Wait()
	if refreshes.Load() != 1 {
		t.Fatalf("parallel ranges refreshed credential %d times", refreshes.Load())
	}
}
