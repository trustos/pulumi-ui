package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/trustos/pulumi-ui/internal/auth"
	"github.com/trustos/pulumi-ui/internal/oci"
)

// RouterConfig holds all handler groups wired in main.go.
type RouterConfig struct {
	Auth       *AuthHandler
	Identity   *IdentityHandler
	Stacks     *StackHandler
	Blueprints *BlueprintHandler
	Network    *NetworkHandler
	Platform   *PlatformHandler
	Admin      *AdminHandler
}

func NewRouter(cfg RouterConfig, frontendFS http.FileSystem) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(cfg.Network.ForwardSubdomainProxy)
	r.Route("/api", func(r chi.Router) {
		// Agent binary download — no auth (instances download at boot)
		r.Get("/agent/binary/{os}/{arch}", cfg.Platform.ServeAgentBinary)
		r.Get("/agent/binary/{os}", cfg.Platform.ServeAgentBinary)

		// OCI schema — no authentication required
		r.Get("/oci-schema", oci.SchemaHandler)
		r.Post("/oci-schema/refresh", oci.SchemaRefreshHandler)

		// Auth — no authentication required
		r.Get("/auth/status", cfg.Auth.AuthStatus)
		r.Post("/auth/register", cfg.Auth.Register)
		r.Post("/auth/login", cfg.Auth.Login)
		r.Post("/auth/import", cfg.Admin.ImportSetup)

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth(cfg.Auth.Users, cfg.Auth.Sessions))

			r.Post("/auth/logout", cfg.Auth.Logout)
			r.Get("/auth/me", cfg.Auth.Me)

			// OCI Accounts
			r.Get("/accounts", cfg.Identity.ListAccounts)
			r.Post("/accounts", cfg.Identity.CreateAccount)
			r.Get("/accounts/export", cfg.Admin.ExportAccounts)
			r.Post("/accounts/generate-keypair", cfg.Network.GenerateKeyPair)
			r.Post("/accounts/import/preview/upload", cfg.Admin.ImportPreviewUpload)
			r.Post("/accounts/import/preview/zip", cfg.Admin.ImportPreviewZip)
			r.Post("/accounts/import/confirm/upload", cfg.Admin.ImportConfirmUpload)
			r.Post("/accounts/import/confirm/zip", cfg.Admin.ImportConfirmZip)
			r.Get("/accounts/{id}", cfg.Identity.GetAccount)
			r.Put("/accounts/{id}", cfg.Identity.UpdateAccount)
			r.Delete("/accounts/{id}", cfg.Identity.DeleteAccount)
			r.Post("/accounts/{id}/verify", cfg.Identity.VerifyAccount)
			r.Get("/accounts/{id}/shapes", cfg.Identity.ListShapes)
			r.Get("/accounts/{id}/images", cfg.Identity.ListImages)
			r.Get("/accounts/{id}/compartments", cfg.Identity.ListCompartments)
			r.Get("/accounts/{id}/availability-domains", cfg.Identity.ListAvailabilityDomains)

			// Blueprints (built-in + custom YAML)
			r.Get("/blueprints", cfg.Blueprints.ListBlueprints)
			r.Post("/blueprints", cfg.Blueprints.CreateBlueprint)
			r.Post("/blueprints/validate", cfg.Blueprints.ValidateBlueprintHandler)
			r.Get("/blueprints/{name}", cfg.Blueprints.GetBlueprint)
			r.Put("/blueprints/{name}", cfg.Blueprints.UpdateBlueprint)
			r.Delete("/blueprints/{name}", cfg.Blueprints.DeleteBlueprint)
			r.Post("/blueprints/{name}/fork", cfg.Blueprints.ForkBlueprint)

			// Programs — backwards compatibility aliases
			r.Get("/programs", cfg.Blueprints.ListBlueprints)
			r.Post("/programs", cfg.Blueprints.CreateBlueprint)
			r.Post("/programs/validate", cfg.Blueprints.ValidateBlueprintHandler)
			r.Get("/programs/{name}", cfg.Blueprints.GetBlueprint)
			r.Put("/programs/{name}", cfg.Blueprints.UpdateBlueprint)
			r.Delete("/programs/{name}", cfg.Blueprints.DeleteBlueprint)
			r.Post("/programs/{name}/fork", cfg.Blueprints.ForkBlueprint)

			// Deployment groups
			r.Get("/groups", cfg.Platform.ListGroups)
			r.Post("/groups", cfg.Platform.CreateGroup)
			r.Get("/groups/{id}", cfg.Platform.GetGroup)
			r.Post("/groups/{id}/deploy", cfg.Platform.DeployGroup)
			r.Post("/groups/{id}/cancel", cfg.Platform.CancelGroupDeploy)
			r.Delete("/groups/{id}", cfg.Platform.DeleteGroup)

			r.Get("/stacks", cfg.Stacks.ListStacks)
			r.Get("/stacks/discover", cfg.Platform.DiscoverRemoteStacks)
			r.Delete("/stacks/discover/{project}/{stack}", cfg.Platform.DeleteRemoteStack)
			r.Post("/stacks/discover/{name}/unlock", cfg.Platform.UnlockRemoteStack)
			r.Put("/stacks/{name}", cfg.Stacks.PutStack)
			r.Delete("/stacks/{name}", cfg.Stacks.DeleteStack)
			r.Get("/stacks/{name}/info", cfg.Stacks.GetStackInfo)
			r.Get("/stacks/{name}/yaml", cfg.Stacks.ExportStackYAML)
			r.Post("/stacks/{name}/up", cfg.Stacks.StackUp)
			r.Post("/stacks/{name}/destroy", cfg.Stacks.StackDestroy)
			r.Post("/stacks/{name}/refresh", cfg.Stacks.StackRefresh)
			r.Post("/stacks/{name}/preview", cfg.Stacks.StackPreview)
			r.Post("/stacks/{name}/cancel", cfg.Stacks.StackCancel)
			r.Post("/stacks/{name}/unlock", cfg.Stacks.StackUnlock)
			r.Post("/stacks/{name}/deploy-apps", cfg.Stacks.StackDeployApps)
			r.Get("/stacks/{name}/logs", cfg.Stacks.GetStackLogs)

			// Lifecycle hooks
			r.Get("/stacks/{name}/hooks", cfg.Platform.ListHooks)
			r.Post("/stacks/{name}/hooks", cfg.Platform.CreateHook)
			r.Delete("/stacks/{name}/hooks/{hookId}", cfg.Platform.DeleteHook)

			// Agent proxy (routes through Nebula mesh)
			r.Get("/stacks/{name}/agent/health", cfg.Network.AgentHealth)
			r.Get("/stacks/{name}/agent/services", cfg.Network.AgentServices)
			r.Get("/stacks/{name}/agent/nomad-jobs", cfg.Network.AgentNomadJobs)
			r.Post("/stacks/{name}/agent/exec", cfg.Network.AgentExec)
			r.Post("/stacks/{name}/agent/upload", cfg.Network.AgentUpload)
			r.Get("/stacks/{name}/agent/shell", cfg.Network.AgentShell)

			// Mesh config download (for local machine Nebula access)
			r.Get("/stacks/{name}/mesh/config", cfg.Network.DownloadMeshConfig)

			// App domain management (Traefik dynamic config)
			r.Get("/stacks/{name}/app-domains", cfg.Blueprints.ListAppDomains)
			r.Put("/stacks/{name}/app-domains/{appKey}", cfg.Blueprints.SetAppDomain)
			r.Delete("/stacks/{name}/app-domains/{appKey}", cfg.Blueprints.RemoveAppDomain)

			// Port forwarding (TCP proxy through Nebula mesh)
			r.Get("/stacks/{name}/forward", cfg.Network.ListPortForwards)
			r.Post("/stacks/{name}/forward", cfg.Network.StartPortForward)
			r.Delete("/stacks/{name}/forward/{id}", cfg.Network.StopPortForward)

			// Passphrases
			r.Get("/passphrases", cfg.Identity.ListPassphrases)
			r.Post("/passphrases", cfg.Identity.CreatePassphrase)
			r.Get("/passphrases/{id}/value", cfg.Identity.GetPassphraseValue)
			r.Patch("/passphrases/{id}", cfg.Identity.RenamePassphrase)
			r.Delete("/passphrases/{id}", cfg.Identity.DeletePassphrase)

			// SSH Keys
			r.Get("/ssh-keys", cfg.Identity.ListSSHKeys)
			r.Post("/ssh-keys", cfg.Identity.CreateSSHKey)
			r.Delete("/ssh-keys/{id}", cfg.Identity.DeleteSSHKey)
			r.Get("/ssh-keys/{id}/private-key", cfg.Identity.DownloadSSHPrivateKey)

			// Settings & Credentials
			r.Get("/settings", cfg.Platform.GetSettings)
			r.Put("/settings", cfg.Platform.PutSettings)
			r.Post("/settings/test-s3", cfg.Platform.TestS3Connection)
			r.Post("/settings/migrate", cfg.Platform.MigrateState)
			r.Get("/settings/credentials", cfg.Identity.GetCredentials)
			r.Put("/settings/credentials", cfg.Identity.PutCredentials)
			r.Get("/settings/health", cfg.Admin.GetHealth)
			r.Get("/settings/export", cfg.Admin.ExportSetup)

			// Application logs
			r.Get("/logs", cfg.Platform.GetLogs)
			r.Get("/logs/stream", cfg.Platform.StreamLogs)
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
