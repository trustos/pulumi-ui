package oci

import (
	"context"
	"errors"
	"fmt"

	"github.com/trustos/pulumi-ui/internal/cloud"
)

const ProviderID = "oci"

// Provider implements cloud.Provider for Oracle Cloud Infrastructure.
type Provider struct {
	cloud.NoValidator // overridden below; keeps Provider embeddable if someone extends

	client *Client
	region string
}

// Factory is the cloud.ProviderFactory for OCI. It reads the credential
// fields from the envelope: tenancyOCID, userOCID, fingerprint, privateKey.
// Region is taken from creds.Region (the account row's region column).
func Factory(ctx context.Context, creds cloud.Credentials) (cloud.Provider, error) {
	f := creds.Fields
	tenancy := f["tenancyOCID"]
	user := f["userOCID"]
	fingerprint := f["fingerprint"]
	privateKey := f["privateKey"]
	region := creds.Region
	if tenancy == "" || user == "" || fingerprint == "" || privateKey == "" {
		return nil, errors.New("oci: missing one of tenancyOCID, userOCID, fingerprint, privateKey")
	}
	client, err := NewClient(tenancy, user, fingerprint, privateKey, region)
	if err != nil {
		return nil, fmt.Errorf("oci: %w", err)
	}
	return &Provider{client: client, region: region}, nil
}

// RenderComputeConfig is the pure renderer registered alongside the
// factory via Registry.RegisterRenderer. Emits just the VALUE side of
// the shapeConfig assignment — the template wraps it in the key:
//
//	shapeConfig: {{ computeConfig ... }}
//
// Flex shape with values present → `{ ocpus: X, memoryInGbs: Y }` (an
// inline YAML object). Fixed shape or missing values → `null`, which
// Pulumi's OCI provider treats as an unset property and omits from the
// resource input. Keeping the emission on the value side lets the
// visual editor round-trip the property unchanged (bare template
// expressions on their own line get dropped by the serializer).
//
// Uses the .Flex name suffix as the synchronous discriminator — OCI's
// only flex family across every region.
func RenderComputeConfig(region, computeType, cpu, memGiB string) string {
	if !isFlexShape(computeType) {
		return "null"
	}
	if cpu == "" || memGiB == "" {
		return "null"
	}
	return fmt.Sprintf("{ ocpus: %s, memoryInGbs: %s }", cpu, memGiB)
}

func (p *Provider) ID() string          { return ProviderID }
func (p *Provider) DisplayName() string { return "Oracle Cloud Infrastructure" }

func (p *Provider) VerifyCredentials(ctx context.Context) error {
	return p.client.VerifyCredentials(ctx)
}

func (p *Provider) ListRegions(ctx context.Context) ([]cloud.Region, error) {
	// OCI's Identity /regions endpoint is broader than what accounts
	// actually have access to; reporting just the account's configured
	// region is safe and matches what the frontend already expects.
	return []cloud.Region{{ID: p.region}}, nil
}

func (p *Provider) ListComputeTypes(ctx context.Context, region string) ([]cloud.ComputeType, error) {
	shapes, err := p.client.ListShapes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]cloud.ComputeType, 0, len(shapes))
	seen := map[string]struct{}{}
	for _, s := range shapes {
		if _, dup := seen[s.Shape]; dup {
			continue
		}
		seen[s.Shape] = struct{}{}
		out = append(out, shapeToComputeType(s))
	}
	return out, nil
}

func (p *Provider) ListImages(ctx context.Context, region, computeType string) ([]cloud.Image, error) {
	imgs, err := p.client.ListImages(ctx, computeType)
	if err != nil {
		return nil, err
	}
	out := make([]cloud.Image, 0, len(imgs))
	for _, im := range imgs {
		out = append(out, imageToCloudImage(im))
	}
	return out, nil
}

func (p *Provider) ListNamespaces(ctx context.Context, region string) ([]cloud.Namespace, error) {
	comps, err := p.client.ListCompartments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]cloud.Namespace, 0, len(comps))
	for _, c := range comps {
		out = append(out, compartmentToNamespace(c))
	}
	return out, nil
}

func (p *Provider) ListZones(ctx context.Context, region string) ([]cloud.Zone, error) {
	ads, err := p.client.ListAvailabilityDomains(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]cloud.Zone, 0, len(ads))
	for _, a := range ads {
		out = append(out, adToZone(a))
	}
	return out, nil
}
