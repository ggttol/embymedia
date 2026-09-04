package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

//go:embed dist/*
var distFS embed.FS

// RegisterWebUI registers SPA static asset serving and client-side route fallback
func RegisterWebUI(e *echo.Echo) error {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return err
	}

	fileServer := http.FileServer(http.FS(sub))

	e.GET("/*", echo.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// If file exists in dist, serve it
		if f, err := sub.Open(path); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Otherwise fallback to index.html for SPA client-side routing
		indexFile, err := sub.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer indexFile.Close()

		stat, _ := indexFile.Stat()
		http.ServeContent(w, r, "index.html", stat.ModTime(), indexFile.(readSeeker))
	})))

	return nil
}

type readSeeker interface {
	fs.File
	Seek(offset int64, whence int) (int64, error)
}
