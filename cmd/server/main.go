package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/embymedia/embymedia/internal/api"
	"github.com/embymedia/embymedia/internal/mcp"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/labstack/echo/v4"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func main() {
	port := flag.String("port", "8080", "HTTP port to listen on")
	mcpPort := flag.String("mcp-port", "8081", "MCP SSE port to listen on")
	dbPath := flag.String("db", "embymedia.db", "SQLite database file path")
	resourceURL := flag.String("resource-url", "http://127.0.0.1:8100", "Resource Index API URL")
	resourceToken := flag.String("resource-token", "", "Resource Index API Token")
	flag.Parse()

	log.Printf("Starting EmbyMedia v2 on :%s (MCP: :%s)...", *port, *mcpPort)

	// 1. Initialize SQLite Database
	db, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// 2. Initialize Domain Services
	driveService := service.NewDriveService(db, *resourceURL, *resourceToken)
	embyService := service.NewEmbyService(db)
	cloudDriveService := service.NewCloudDriveService(db)
	taskQueue := service.NewTaskQueueService(db, driveService, embyService)
	cronManager := service.NewCronManager(db)
	settingsService := service.NewSettingsService(db)

	// 3. Initialize Unified Echo HTTP Server
	e := echo.New()
	e.HideBanner = true
	apiServer := api.NewServer(e, db, driveService, embyService, cloudDriveService, taskQueue, cronManager, settingsService)
	// Mount embedded Web UI
	_ = RegisterWebUI(e)

	// 4. Initialize MCP Server
	mcpServer := mcp.NewMCPServer(db, driveService, embyService, cloudDriveService, taskQueue)

	// 5. Start MCP SSE Server in background
	go func() {
		sseServer := mcpserver.NewSSEServer(mcpServer.Server())
		mcpMux := http.NewServeMux()
		mcpMux.Handle("/sse", sseServer)
		mcpMux.HandleFunc("/tools", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","service":"embymedia-mcp"}`))
		})
		log.Printf("EmbyMedia MCP SSE server listening on :%s", *mcpPort)
		if err := http.ListenAndServe(":"+*mcpPort, mcpMux); err != nil && err != http.ErrServerClosed {
			log.Printf("MCP server error: %v", err)
		}
	}()

	// 6. Graceful Shutdown
	go func() {
		if err := apiServer.Start(":" + *port); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down servers...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = e.Shutdown(ctx)
	log.Println("EmbyMedia stopped cleanly.")
}
