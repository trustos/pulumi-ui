package cloud

import "context"

// Provider is the provider-neutral cloud-metadata surface. Concrete
// implementations live in internal/cloud/<providerID>/.
type Provider interface {
	ID() string
	DisplayName() string

	VerifyCredentials(ctx context.Context) error

	ListRegions(ctx context.Context) ([]Region, error)
	ListComputeTypes(ctx context.Context, region string) ([]ComputeType, error)
	ListImages(ctx context.Context, region, computeType string) ([]Image, error)
	ListNamespaces(ctx context.Context, region string) ([]Namespace, error)
	ListZones(ctx context.Context, region string) ([]Zone, error)

	// Validate runs provider-specific runtime rules against the rendered
	// resource graph. Providers with no runtime rules embed NoValidator
	// and inherit a zero-cost no-op.
	Validate(ctx context.Context, graph ResourceGraph, ref AccountRef) []ValidationError
}

// ComputeConfigRenderer emits the YAML fragment that the blueprint
// template helper {{ computeConfig }} substitutes. Returning the empty
// string signals "no shapeConfig needed for this compute type". Pure
// function — no network calls, no credentials — so the template helper
// can invoke it without building a Provider.
type ComputeConfigRenderer func(region, computeType, cpu, memGiB string) string

// NoValidator is an embeddable default for providers without runtime
// rules. Validate returns nil.
type NoValidator struct{}

func (NoValidator) Validate(ctx context.Context, graph ResourceGraph, ref AccountRef) []ValidationError {
	return nil
}

// ProviderFactory constructs a Provider from a credential envelope.
// Factories live alongside the provider package and register into the
// Registry at server startup.
type ProviderFactory func(ctx context.Context, creds Credentials) (Provider, error)
