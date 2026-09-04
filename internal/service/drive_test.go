package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/embymedia/embymedia/internal/domain"
	"github.com/embymedia/embymedia/internal/storage"
)

func TestDriveServiceMock(t *testing.T) {
	// Mock 115 webapi server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/files" {
			w.Write([]byte(`{"state":true,"count":2,"data":[{"fid":123,"pid":0,"n":"Movie.mp4","s":1024000,"pc":"pick1","sha":"sha123","t":"2026-09-04"},{"cid":456,"pid":0,"n":"Series","s":0,"pc":"","sha":"","t":"2026-09-04"}]}`))
			return
		}
		if r.URL.Path == "/web/lixian/" {
			w.Write([]byte(`{"state":true,"errno":0,"error_msg":"","info_hash":"hash123","name":"Test Movie"}`))
			return
		}
		w.Write([]byte(`{"state":true}`))
	}))
	defer server.Close()

	dbFile := "/tmp/test_drive_srv.db"
	_ = os.Remove(dbFile)
	defer os.Remove(dbFile)

	db, err := storage.Open(dbFile)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_ = db.SaveAccount(&domain.DriveAccount{
		ID:        "acc-1",
		Type:      "115",
		Name:      "Main 115",
		Cookie:    "test-cookie",
		IsDefault: true,
	})

	svc := NewDriveService(db, "http://127.0.0.1:8100", "")
	acc, err := svc.GetDefaultAccount()
	if err != nil || acc.Name != "Main 115" {
		t.Fatalf("expected default account, got %v: %v", acc, err)
	}
}
