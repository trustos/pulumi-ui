package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/trustos/pulumi-ui/internal/auth"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
	"github.com/trustos/pulumi-ui/internal/logbuffer"
	"github.com/trustos/pulumi-ui/internal/mesh"
	"github.com/trustos/pulumi-ui/internal/oci"
	"github.com/trustos/pulumi-ui/internal/programs"
)

// Handler holds all dependencies wired in main.go.
type Handler struct {
	DB             *sql.DB
	Creds          *db.CredentialStore
	Ops            *db.OperationStore
	Stacks         *db.StackStore
	Users          *db.UserStore
	Sessions       *db.SessionStore
	Accounts       *db.AccountStore
	Passphrases    *db.PassphraseStore
	SSHKeys        *db.SSHKeyStore
	CustomPrograms *db.CustomProgramStore
	Engine         *engine.Engine
	Registry       *programs.ProgramRegistry
	ConnStore      *db.StackConnectionStore
	MeshManager    *mesh.Manager
	LogBuffer      *logbuffer.Buffer
}

func NewHandler(
	sqlDB *sql.DB,
	creds *db.CredentialStore,
	ops *db.OperationStore,
	stacks *db.StackStore,
	users *db.UserStore,
	sessions *db.SessionStore,
	accounts *db.AccountStore,
	passphrases *db.PassphraseStore,
	sshKeys *db.SSHKeyStore,
	customPrograms *db.CustomProgramStore,
	eng *engine.Engine,
	registry *programs.ProgramRegistry,
	connStore *db.StackConnectionStore,
) *Handler {
	return &Handler{
		DB:             sqlDB,
		Creds:          creds,
		Ops:            ops,
		Stacks:         stacks,
		Users:          users,
		Sessions:       sessions,
		Accounts:       accounts,
		Passphrases:    passphrases,
		SSHKeys:        sshKeys,
		CustomPrograms: customPrograms,
		Engine:         eng,
		Registry:       registry,
		ConnStore:      connStore,
	}
}

func NewRouter(h *Handler, frontendFS http.FileSystem) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	r.Route("/api", func(r chi.Router) {
		// Agent binary download — no auth (instances download at boot)
		r.Get("/agent/binary/{os}/{arch}", h.ServeAgentBinary)
		r.Get("/agent/binary/{os}", h.ServeAgentBinary)

		// OCI schema — no authentication required
		r.Get("/oci-schema", oci.SchemaHandler)
		r.Post("/oci-schema/refresh", oci.SchemaRefreshHandler)

		// Auth — no authentication required
		r.Get("/auth/status", h.AuthStatus)
		r.Post("/auth/register", h.Register)
		r.Post("/auth/login", h.Login)

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth(h.Users, h.Sessions))

			r.Post("/auth/logout", h.Logout)
			r.Get("/auth/me", h.Me)

			// OCI Accounts
			r.Get("/accounts", h.ListAccounts)
			r.Post("/accounts", h.CreateAccount)
			r.Get("/accounts/export", h.ExportAccounts)
			r.Post("/accounts/generate-keypair", h.GenerateKeyPair)
			r.Post("/accounts/import/preview/upload", h.ImportPreviewUpload)
			r.Post("/accounts/import/preview/zip", h.ImportPreviewZip)
			r.Post("/accounts/import/confirm/upload", h.ImportConfirmUpload)
			r.Post("/accounts/import/confirm/zip", h.ImportConfirmZip)
			r.Get("/accounts/{id}", h.GetAccount)
			r.Put("/accounts/{id}", h.UpdateAccount)
			r.Delete("/accounts/{id}", h.DeleteAccount)
			r.Post("/accounts/{id}/verify", h.VerifyAccount)
			r.Get("/accounts/{id}/shapes", h.ListShapes)
			r.Get("/accounts/{id}/images", h.ListImages)
			r.Get("/accounts/{id}/compartments", h.ListCompartments)
			r.Get("/accounts/{id}/availability-domains", h.ListAvailabilityDomains)

			// Programs (built-in + custom YAML)
			r.Get("/programs", h.ListPrograms)
			r.Post("/programs", h.CreateProgram)
			r.Post("/programs/validate", h.ValidateProgramHandler)
			r.Get("/programs/{name}", h.GetProgram)
			r.Put("/programs/{name}", h.UpdateProgram)
			r.Delete("/programs/{name}", h.DeleteProgram)
			r.Post("/programs/{name}/fork", h.ForkProgram)
			r.Get("/stacks", h.ListStacks)
			r.Put("/stacks/{name}", h.PutStack)
			r.Delete("/stacks/{name}", h.DeleteStack)
			r.Get("/stacks/{name}/info", h.GetStackInfo)
			r.Get("/stacks/{name}/yaml", h.ExportStackYAML)
			r.Post("/stacks/{name}/up", h.StackUp)
			r.Post("/stacks/{name}/destroy", h.StackDestroy)
			r.Post("/stacks/{name}/refresh", h.StackRefresh)
			r.Post("/stacks/{name}/preview", h.StackPreview)
			r.Post("/stacks/{name}/cancel", h.StackCancel)
			r.Post("/stacks/{name}/unlock", h.StackUnlock)
			r.Post("/stacks/{name}/deploy-apps", h.StackDeployApps)
			r.Get("/stacks/{name}/logs", h.GetStackLogs)

			// Agent proxy (routes through Nebula mesh)
			r.Get("/stacks/{name}/agent/health", h.AgentHealth)
			r.Get("/stacks/{name}/agent/services", h.AgentServices)
			r.Post("/stacks/{name}/agent/exec", h.AgentExec)
			r.Post("/stacks/{name}/agent/upload", h.AgentUpload)
			r.Get("/stacks/{name}/agent/shell", h.AgentShell)

			// Passphrases
			r.Get("/passphrases", h.ListPassphrases)
			r.Post("/passphrases", h.CreatePassphrase)
			r.Patch("/passphrases/{id}", h.RenamePassphrase)
			r.Delete("/passphrases/{id}", h.DeletePassphrase)

			// SSH Keys
			r.Get("/ssh-keys", h.ListSSHKeys)
			r.Post("/ssh-keys", h.CreateSSHKey)
			r.Delete("/ssh-keys/{id}", h.DeleteSSHKey)
			r.Get("/ssh-keys/{id}/private-key", h.DownloadSSHPrivateKey)

			// Settings & Credentials
			r.Get("/settings", h.GetSettings)
			r.Put("/settings", h.PutSettings)
			r.Get("/settings/credentials", h.GetCredentials)
			r.Put("/settings/credentials", h.PutCredentials)
			r.Get("/settings/health", h.GetHealth)

			// Application logs
			r.Get("/logs", h.GetLogs)
			r.Get("/logs/stream", h.StreamLogs)
		})
	})

	// Serve embedded Svelte SPA — all non-API routes return index.html.
	r.Handle("/*", spaHandler(frontendFS))

	return r
}

// spaHandler serves static files and falls back to index.html for client-side routing.
func spaHandler(fs http.FileSystem) http.Handler {
	fileServer := http.FileServer(fs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := fs.Open(r.URL.Path)
		if err != nil {
			r.URL.Path = "/"
		} else {
			f.Close()
		}
		fileServer.ServeHTTP(w, r)
	})
}
