package cloud

import (
	"context"
	"fmt"
	"sync"
)

// AccountResolver is the narrow interface the Registry uses to resolve
// an AccountRef to concrete credentials. Adapters live outside the
// cloud package and bridge the app's account store.
type AccountResolver interface {
	Resolve(accountID string) (ResolvedAccount, error)
}

// ResolvedAccount is what the resolver returns for an account ID.
// CredentialsFingerprint is an opaque stable-under-credentials-identity
// value that changes when the underlying credentials rotate (for OCI
// the API-key fingerprint is the natural choice).
type ResolvedAccount struct {
	ProviderID             string
	CredentialsFingerprint string
	Credentials            Credentials
}

// Registry is a dependency-injected collection of provider factories
// plus a per-account Provider cache. One instance is constructed in
// main.go and threaded through handlers and services.
type Registry struct {
	resolver AccountResolver

	mu        sync.Mutex
	factories map[string]ProviderFactory
	renderers map[string]ComputeConfigRenderer

	cache sync.Map // providerCacheKey → Provider
}

type providerCacheKey struct {
	TenantID    string
	AccountID   string
	Fingerprint string
}

func NewRegistry(resolver AccountResolver) *Registry {
	return &Registry{
		resolver:  resolver,
		factories: map[string]ProviderFactory{},
		renderers: map[string]ComputeConfigRenderer{},
	}
}

// Register associates a provider ID with a factory. Usually called
// once per known provider at server startup.
func (r *Registry) Register(id string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[id] = factory
}

// RegisterRenderer associates a provider ID with its pure compute-config
// renderer. Called once per provider at startup, alongside Register.
func (r *Registry) RegisterRenderer(id string, renderer ComputeConfigRenderer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renderers[id] = renderer
}

// RenderComputeConfig looks up the renderer for providerID and invokes
// it. Unknown providers return the empty string — the template helper
// treats this as "no shapeConfig", the safe default.
func (r *Registry) RenderComputeConfig(providerID, region, computeType, cpu, memGiB string) string {
	r.mu.Lock()
	fn, ok := r.renderers[providerID]
	r.mu.Unlock()
	if !ok {
		return ""
	}
	return fn(region, computeType, cpu, memGiB)
}

// ProviderFor resolves ref to a Provider, constructing via the
// registered factory on cache miss. The cache key includes TenantID so
// multi-tenant retrofits do not require a cross-cutting audit.
func (r *Registry) ProviderFor(ctx context.Context, ref AccountRef) (Provider, error) {
	acc, err := r.resolver.Resolve(ref.AccountID)
	if err != nil {
		return nil, err
	}
	key := providerCacheKey{
		TenantID:    ref.TenantID,
		AccountID:   ref.AccountID,
		Fingerprint: acc.CredentialsFingerprint,
	}
	if v, ok := r.cache.Load(key); ok {
		return v.(Provider), nil
	}

	r.mu.Lock()
	factory, known := r.factories[acc.ProviderID]
	r.mu.Unlock()
	if !known {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotFound, acc.ProviderID)
	}
	p, err := factory(ctx, acc.Credentials)
	if err != nil {
		return nil, err
	}
	actual, _ := r.cache.LoadOrStore(key, p)
	return actual.(Provider), nil
}

// Invalidate drops any cached Provider for the given account, regardless
// of fingerprint. Call from the account Update/Delete handlers.
func (r *Registry) Invalidate(ref AccountRef) {
	r.cache.Range(func(k, _ any) bool {
		key := k.(providerCacheKey)
		if key.TenantID == ref.TenantID && key.AccountID == ref.AccountID {
			r.cache.Delete(key)
		}
		return true
	})
}
