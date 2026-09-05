package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/embymedia/embymedia/internal/api"
	"github.com/embymedia/embymedia/internal/mcp"
	"github.com/embymedia/embymedia/internal/security"
	"github.com/embymedia/embymedia/internal/service"
	"github.com/embymedia/embymedia/internal/storage"
	"github.com/labstack/echo/v4"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func authenticateMCPHTTP(authorizer *security.AgentAuthorizer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if security.TrustedWithoutToken(request.Header) {
			next.ServeHTTP(response, request)
			return
		}
		secret := security.BearerToken(request.Header)
		if secret == "" {
			http.Error(response, "agent token is required", http.StatusUnauthorized)
			return
		}
		if _, err := authorizer.Authenticate(secret); err != nil {
			http.Error(response, err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(response, request)
	})
}
func main() {
	port := flag.String("port", envOr("EMBYMEDIA_PORT", "8080"), "HTTP port to listen on")
	host := flag.String("host", envOr("EMBYMEDIA_HOST", "127.0.0.1"), "HTTP address to listen on")
	mcpPort := flag.String("mcp-port", envOr("EMBYMEDIA_MCP_PORT", "8081"), "legacy MCP SSE port to listen on")
	mcpHost := flag.String("mcp-host", envOr("EMBYMEDIA_MCP_HOST", "127.0.0.1"), "legacy MCP SSE address to listen on")
	dbPath := flag.String("db", envOr("EMBYMEDIA_DB", "embymedia.db"), "SQLite database file path")
	mcpMode := flag.Bool("mcp", false, "serve MCP over stdio instead of HTTP")
	checkDB := flag.Bool("check-db", false, "open, migrate, and close the database without starting services")
	webhookSecretFile := flag.String("bootstrap-webhook-secret-file", "", "persist a webhook secret from a file and exit")
	resourceURL := flag.String("resource-url", envOr("RESOURCE_INDEX_URL", "http://127.0.0.1:8100"), "Resource Index API URL")
	resourceToken := flag.String("resource-token", envOr("RESOURCE_INDEX_TOKEN", ""), "Resource Index API token")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	owner, err := lockDatabase(*dbPath)
	if err != nil {
		log.Fatalf("claim database: %v", err)
	}
	if owner != nil {
		defer owner.Close()
	}
	db, err := storage.Open(*dbPath)
	if err != nil {
		log.Fatalf("initialize database: %v", err)
	}
	defer db.Close()
	if *checkDB {
		return
	}
	if *webhookSecretFile != "" {
		secret, err := os.ReadFile(*webhookSecretFile)
		if err != nil {
			log.Fatalf("read webhook secret: %v", err)
		}
		value := strings.TrimSpace(string(secret))
		if value == "" {
			log.Fatal("webhook secret file is empty")
		}
		if err := db.SetSetting("clouddrive_webhook_secret", value); err != nil {
			log.Fatalf("persist webhook secret: %v", err)
		}
		return
	}
	authorizer := security.NewAgentAuthorizer(db)

	driveService := service.NewDriveService(db, *resourceURL, *resourceToken)
	embyService := service.NewEmbyService(db)
	cloudDriveService := service.NewCloudDriveService(db)
	taskQueue := service.NewTaskQueueService(db, driveService, embyService)
	if err := taskQueue.Start(ctx); err != nil {
		log.Fatalf("start task queue: %v", err)
	}
	defer taskQueue.Stop()
	cronManager := service.NewCronManager(db, taskQueue)
	if err := cronManager.Start(ctx); err != nil {
		log.Fatalf("start scheduler: %v", err)
	}
	settingsService := service.NewSettingsService(db)
	defer cronManager.Stop()

	e := echo.New()
	e.HideBanner = true
	apiServer := api.NewServer(e, db, driveService, embyService, cloudDriveService, taskQueue, cronManager, settingsService, authorizer)
	defer apiServer.Close()
	mcpServer := mcp.NewMCPServer(db, driveService, embyService, cloudDriveService, taskQueue, cronManager, authorizer, *mcpMode)
	if *mcpMode {
		if err := mcpserver.ServeStdio(mcpServer.Server()); err != nil && ctx.Err() == nil {
			log.Fatalf("MCP stdio server: %v", err)
		}
		return
	}

	streamableMCP := mcpserver.NewStreamableHTTPServer(
		mcpServer.Server(),
		mcpserver.WithDisableLocalhostProtection(true),
	)
	e.Any("/mcp", echo.WrapHandler(authenticateMCPHTTP(authorizer, streamableMCP)))
	if err := RegisterWebUI(e); err != nil {
		log.Fatalf("register Web UI: %v", err)
	}

	sseMCP := mcpserver.NewSSEServer(mcpServer.Server())
	mcpMux := http.NewServeMux()
	mcpMux.Handle("/", authenticateMCPHTTP(authorizer, sseMCP))
	legacyAddr := net.JoinHostPort(*mcpHost, *mcpPort)
	legacyServer := &http.Server{Addr: legacyAddr, Handler: mcpMux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 2)
	go func() {
		httpAddr := net.JoinHostPort(*host, *port)
		log.Printf("EmbyMedia HTTP and Streamable MCP listening on %s", httpAddr)
		if err := apiServer.Start(httpAddr); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTP server: %w", err)
		}
	}()
	go func() {
		log.Printf("EmbyMedia legacy MCP SSE listening on %s", legacyAddr)
		if err := legacyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("legacy MCP SSE server: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		log.Printf("server stopped unexpectedly: %v", err)
		stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
	if err := legacyServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("legacy MCP shutdown: %v", err)
	}
}
