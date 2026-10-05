package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/lowcarbdev/sbv/internal"
	"golang.org/x/term"
	"golang.org/x/time/rate"
)

var logger *slog.Logger

func main() {
	// Parse CLI flags
	resetPassword := flag.String("reset-password", "", "Reset password for the specified username")
	listUsers := flag.Bool("list-users", false, "List all users")
	journalMode := flag.Bool("journal", false, "Use rollback journal mode instead of WAL (for network filesystems)")
	flag.Parse()

	// Use WAL mode by default, unless disabled via the -journal flag or the
	// SQLITE_MODE env var (useful for network filesystems, which don't
	// support WAL). SQLITE_MODE=journal disables WAL.
	internal.UseWALMode = !*journalMode
	if mode := os.Getenv("SQLITE_MODE"); mode != "" {
		internal.UseWALMode = !strings.EqualFold(mode, "journal")
	}

	// Initialize slog logger
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	dbPathPrefix := os.Getenv("DB_PATH_PREFIX")
	if dbPathPrefix == "" {
		dbPathPrefix = "."
	}

	// Redirect os.TempDir() (honors TMPDIR) to the data volume, before
	// anything in the standard library or its dependencies has a chance to
	// use the default (the container's own, often small, root filesystem).
	// The main culprit is net/http's multipart form parser: for uploads
	// larger than the in-memory threshold it spills the overflow to
	// os.TempDir() internally, before our own upload-handling code ever
	// runs -- entirely uncontrolled by DB_PATH_PREFIX otherwise. A large
	// backup upload could fill the container's root filesystem even though
	// the data volume (where we explicitly stage uploads ourselves too) has
	// plenty of room.
	tmpDir := filepath.Join(dbPathPrefix, "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		logger.Error("Failed to create temp directory on data volume", "path", tmpDir, "error", err)
		os.Exit(1)
	}
	if err := os.Setenv("TMPDIR", tmpDir); err != nil {
		logger.Error("Failed to set TMPDIR", "error", err)
		os.Exit(1)
	}

	// Initialize authentication database
	authDBPath := dbPathPrefix + "/sbv.db"

	err := internal.InitAuthDB(authDBPath)
	if err != nil {
		logger.Error("Failed to initialize authentication database", "error", err)
		os.Exit(1)
	}
	logger.Info("Authentication database initialized", "path", authDBPath)

	// Handle password reset if requested
	if *resetPassword != "" {
		if err := handleResetPassword(*resetPassword); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Handle list users if requested
	if *listUsers {
		if err := handleListUsers(dbPathPrefix); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Periodically clean up expired sessions
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			if err := internal.CleanExpiredSessions(); err != nil {
				logger.Error("Failed to clean expired sessions", "error", err)
			}
			<-ticker.C
		}
	}()

	// Create Echo instance
	e := echo.New()

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.GzipWithConfig(middleware.GzipConfig{Level: 5}))

	// Use custom CORS middleware that properly handles credentials
	e.Use(internal.CustomCORSMiddleware())

	// Configure timeouts for large file uploads
	e.Server.ReadTimeout = 30 * time.Minute
	e.Server.WriteTimeout = 30 * time.Minute
	e.Server.ReadHeaderTimeout = 1 * time.Minute
	e.Server.IdleTimeout = 2 * time.Minute
	e.Server.MaxHeaderBytes = 1 << 20 // 1 MB max header size

	// Rate limit login/register per IP to slow credential brute-forcing:
	// bursts of 5 attempts, refilling one attempt every 10 seconds
	authRateLimiter := middleware.RateLimiter(middleware.NewRateLimiterMemoryStoreWithConfig(
		middleware.RateLimiterMemoryStoreConfig{
			Rate:      rate.Limit(0.1),
			Burst:     5,
			ExpiresIn: 10 * time.Minute,
		},
	))

	// Public routes (no authentication required)
	// Apply NoCacheMiddleware to prevent browser caching of auth responses
	e.POST("/api/auth/register", internal.HandleRegister, internal.NoCacheMiddleware, authRateLimiter)
	e.POST("/api/auth/login", internal.HandleLogin, internal.NoCacheMiddleware, authRateLimiter)

	// OIDC single sign-on (enabled when OIDC_ISSUER_URL is set)
	if internal.OIDCEnabled() {
		e.GET("/api/auth/oidc/login", internal.HandleOIDCLogin, internal.NoCacheMiddleware, authRateLimiter)
		e.GET("/api/auth/oidc/callback", internal.HandleOIDCCallback, internal.NoCacheMiddleware, authRateLimiter)
		logger.Info("OIDC login enabled", "issuer", os.Getenv("OIDC_ISSUER_URL"), "provider", internal.OIDCProviderName())
	}
	e.POST("/api/auth/logout", internal.HandleLogout, internal.NoCacheMiddleware)

	// Protected routes (authentication required)
	protected := e.Group("/api")
	protected.Use(internal.AuthMiddleware)
	protected.Use(internal.NoCacheMiddleware) // Prevent browser caching of API responses

	protected.GET("/auth/me", internal.HandleMe)
	protected.POST("/auth/change-password", internal.HandleChangePassword)
	protected.POST("/upload", internal.HandleUpload)

	// Extract Media routes
	protected.POST("/extract-media", internal.HandleExtractMedia)
	protected.GET("/extract-media/progress", internal.HandleExtractMediaProgress)
	protected.POST("/extract-media/open-folder", internal.HandleOpenMediaFolder)
	protected.POST("/extract-media/browse-folder", internal.HandleBrowseFolder)
	protected.POST("/extract-media/browse-file", internal.HandleBrowseXMLFile)

	// Merge Backups routes
	protected.POST("/merge-backups", internal.HandleStartMergeBackups)
	protected.GET("/merge-backups/progress", internal.HandleGetMergeProgress)
	protected.POST("/merge-backups/browse-folder", internal.HandleBrowseMergeFolder)
	protected.POST("/merge-backups/browse-save-file", internal.HandleBrowseMergeSaveFile)
	protected.POST("/merge-backups/open-output", internal.HandleOpenMergedFileFolder)
	protected.POST("/merge-backups/detect-numbers", internal.HandleDetectMergeNumbers)

	protected.GET("/conversations", internal.HandleConversations)
	protected.GET("/messages", internal.HandleMessages)
	protected.GET("/activity", internal.HandleActivity)
	protected.GET("/calls", internal.HandleCalls)
	protected.GET("/daterange", internal.HandleDateRange)
	protected.GET("/progress", internal.HandleProgress)
	protected.GET("/media", internal.HandleMedia)
	protected.GET("/media-items", internal.HandleMediaItems)
	protected.GET("/search", internal.HandleSearch)
	protected.GET("/settings", internal.HandleGetSettings)
	protected.PUT("/settings", internal.HandleUpdateSettings)
	protected.GET("/analytics", internal.HandleAnalytics)

	// Health check
	e.GET("/api/health", func(c echo.Context) error {
		return c.String(http.StatusOK, "OK")
	})

	// Version endpoint (public, no authentication required)
	e.GET("/api/version", internal.HandleVersion)

	// Public config endpoint (e.g. whether registration is enabled)
	e.GET("/api/config", internal.HandleConfig, internal.NoCacheMiddleware)

	// Serve static files from frontend/dist if it exists (for production/Docker)
	if _, err := os.Stat("./frontend/dist"); err == nil {
		// Serve static assets (JS, CSS, images, etc.)
		e.Static("/assets", "./frontend/dist/assets")
		e.File("/favicon.ico", "./frontend/dist/favicon.ico")
		e.File("/favicon.svg", "./frontend/dist/favicon.svg")
		e.File("/apple-touch-icon.png", "./frontend/dist/apple-touch-icon.png")
		e.File("/favicon-96x96.png", "./frontend/dist/favicon-96x96.png")
		e.File("/web-app-manifest-192x192.png", "./frontend/dist/web-app-manifest-192x192.png")
		e.File("/web-app-manifest-512x512.png", "./frontend/dist/web-app-manifest-512x512.png")
		e.File("/site.webmanifest", "./frontend/dist/site.webmanifest")

		// SPA fallback - serve index.html for all non-API routes
		// This must be last so it doesn't interfere with API routes
		e.GET("/*", func(c echo.Context) error {
			return c.File("./frontend/dist/index.html")
		})

		logger.Info("Serving static files from ./frontend/dist with SPA routing support")
	}

	// Start auto-import service
	dataDir := dbPathPrefix + "/data"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		logger.Error("Failed to create data directory", "path", dataDir, "error", err)
	}
	autoImportService := internal.NewAutoImportService(dataDir)
	autoImportService.Start()
	defer autoImportService.Stop()

	// Start pprof server on localhost when profiling is explicitly enabled
	if os.Getenv("PPROF_ENABLED") == "true" {
		go func() {
			pprofAddr := "127.0.0.1:6060"
			logger.Info("Memory profiling available", "url", "http://"+pprofAddr+"/debug/pprof/")
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				logger.Error("pprof server failed", "error", err)
			}
		}()
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}

	bindHost := "127.0.0.1"
	addr := bindHost + ":" + port

	logger.Info("Starting server", "address", addr, "port", port, "bindHost", bindHost)
	if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
		logger.Error("Server failed to start", "error", err)
		os.Exit(1)
	}
}

func handleResetPassword(username string) error {
	var newPassword string
	var confirmPassword string

	fmt.Printf("Enter new password for %s: ", username)
	bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to read password: %w", err)
	}
	fmt.Println()
	newPassword = strings.TrimSpace(string(bytePassword))

	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters long")
	}

	fmt.Printf("Confirm new password: ")
	byteConfirm, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to read password: %w", err)
	}
	fmt.Println()
	confirmPassword = strings.TrimSpace(string(byteConfirm))

	if newPassword != confirmPassword {
		return fmt.Errorf("passwords do not match")
	}

	user, err := internal.GetUserByUsername(username)
	if err != nil {
		return err
	}
	if err := internal.UpdatePassword(user.ID, newPassword); err != nil {
		return fmt.Errorf("failed to reset password: %w", err)
	}

	fmt.Printf("Password successfully reset for user %s\n", username)
	return nil
}

func handleListUsers(dbPathPrefix string) error {
	users, err := internal.ListUsers()
	if err != nil {
		return fmt.Errorf("failed to list users: %w", err)
	}

	if len(users) == 0 {
		fmt.Println("No users found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tUSERNAME\tMESSAGE COUNT\tCREATED AT")
	fmt.Fprintln(w, "--\t--------\t-------------\t----------")

	for _, u := range users {
		msgCount := 0
		userDB, err := internal.GetUserDB(u.ID, u.Username)
		if err == nil {
			var count int
			if err := userDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err == nil {
				msgCount = count
			}
		}

		createdAt := u.CreatedAt.Format("2006-01-02 15:04:05")
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", u.ID, u.Username, msgCount, createdAt)
	}

	return w.Flush()
}
