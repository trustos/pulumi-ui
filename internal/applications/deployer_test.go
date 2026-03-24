package applications

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/trustos/pulumi-ui/internal/programs"
)

func TestFilterWorkloads_Empty(t *testing.T) {
	result := filterWorkloads(nil, nil)
	assert.Empty(t, result)
}

func TestFilterWorkloads_OnlySelectedWorkloads(t *testing.T) {
	catalog := []programs.ApplicationDef{
		{Key: "consul", Name: "Consul", Tier: programs.TierBootstrap},
		{Key: "nomad", Name: "Nomad", Tier: programs.TierBootstrap},
		{Key: "traefik", Name: "Traefik", Tier: programs.TierWorkload},
		{Key: "grafana", Name: "Grafana", Tier: programs.TierWorkload},
		{Key: "vault", Name: "Vault", Tier: programs.TierWorkload},
	}
	selected := map[string]bool{
		"consul":  true,
		"traefik": true,
		"vault":   true,
	}

	result := filterWorkloads(catalog, selected)
	assert.Len(t, result, 2)
	assert.Equal(t, "traefik", result[0].Key)
	assert.Equal(t, "vault", result[1].Key)
}

func TestFilterWorkloads_NoneSelected(t *testing.T) {
	catalog := []programs.ApplicationDef{
		{Key: "app1", Tier: programs.TierWorkload},
		{Key: "app2", Tier: programs.TierWorkload},
	}
	result := filterWorkloads(catalog, map[string]bool{})
	assert.Empty(t, result)
}

func TestFilterWorkloads_BootstrapExcluded(t *testing.T) {
	catalog := []programs.ApplicationDef{
		{Key: "boot1", Tier: programs.TierBootstrap},
	}
	result := filterWorkloads(catalog, map[string]bool{"boot1": true})
	assert.Empty(t, result)
}

func TestNewDeployer(t *testing.T) {
	d := NewDeployer(nil)
	assert.NotNil(t, d)
}
