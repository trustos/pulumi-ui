package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/trustos/pulumi-ui/internal/api"
	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
	"github.com/trustos/pulumi-ui/internal/keystore"

	// Blank imports trigger init() in each program file, registering them.
	_ "github.com/trustos/pulumi-ui/internal/programs"
)

// The frontend/dist/ directory is built by `cd frontend && npm run build`
// before compiling the Go binary. The Dockerfile handles this in a prior stage.
//
//go:embed all:frontend/dist
var frontendDist embed.FS

func main() {
	dataDir := envOr("PULUMI_UI_DATA_DIR", "/data")
	stateDir := envOr("PULUMI_UI_STATE_DIR", dataDir+"/state")

	// Pulumi resolves the backend URL relative to its own WorkDir (os.TempDir),
	// so the state dir must be an absolute path.
	if abs, err := filepath.Abs(stateDir); err == nil {
		stateDir = abs
	}
	listenAddr := envOr("PULUMI_UI_ADDR", ":8080")

	// Resolve encryption key: env var → keystore → auto-generate
	ks, err := keystore.New(dataDir)
	if err != nil {
		log.Fatalf("Key store config error: %v", err)
	}
	encKey, err := keystore.Resolve(ks)
	if err != nil {
		log.Fatalf("Failed to resolve encryption key: %v", err)
	}

	// Encryption
	enc, err := crypto.NewEncryptor(encKey)
	if err != nil {
		log.Fatalf("Invalid encryption key: %v", err)
	}

	// Ensure state directory exists
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		log.Fatalf("Cannot create state dir %s: %v", stateDir, err)
	}

	// Database
	database, err := db.Open(dataDir + "/pulumi-ui.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	// Stores
	creds := db.NewCredentialStore(database, enc)
	ops := db.NewOperationStore(database)

	// Mark any operations that were running when the server last stopped as failed.
	if err := ops.MarkStaleRunning(); err != nil {
		log.Printf("Warning: could not mark stale operations: %v", err)
	}
	stackStore := db.NewStackStore(database)
	users := db.NewUserStore(database)
	sessions := db.NewSessionStore(database)
	accounts := db.NewAccountStore(database, enc)
	passphrases := db.NewPassphraseStore(database, enc)

	// Prune expired sessions on startup
	sessions.DeleteExpired()

	// Engine
	eng := engine.New(stateDir)

	// HTTP handler
	h := api.NewHandler(database, creds, ops, stackStore, users, sessions, accounts, passphrases, eng)

	// Embedded frontend — serve from the embed.FS sub-tree
	sub, err := fs.Sub(frontendDist, "frontend/dist")
	if err != nil {
		log.Fatalf("Failed to create frontend sub-FS: %v", err)
	}
	frontendFS := http.FS(sub)

	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      api.NewRouter(h, frontendFS),
		ReadTimeout:  0, // intentional — SSE streams are long-lived
		WriteTimeout: 0,
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		host := "localhost"
		port := listenAddr
		if len(port) > 0 && port[0] == ':' {
			port = port[1:]
		}
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Printf("  pulumi-ui ready")
		log.Printf("  Backend  →  http://%s:%s", host, port)
		log.Printf("  Frontend →  http://%s:%s  (embedded SPA)", host, port)
		log.Printf("  HMR dev  →  run 'make watch-frontend' and open http://%s:5173", host)
		log.Printf("  Key store → %s", ks.Description())
		log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("Shutdown complete")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
