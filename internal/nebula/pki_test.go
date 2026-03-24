package nebula

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCA(t *testing.T) {
	ca, err := GenerateCA("test-ca", 24*time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, ca.CertPEM)
	assert.NotEmpty(t, ca.KeyPEM)
	assert.Contains(t, string(ca.CertPEM), "NEBULA CERTIFICATE")
}

func TestIssueCert(t *testing.T) {
	ca, err := GenerateCA("test-ca", 48*time.Hour)
	require.NoError(t, err)

	subnet := "10.42.1.0/24"
	uiIP, err := UIAddress(subnet)
	require.NoError(t, err)

	cert, err := IssueCert(ca.CertPEM, ca.KeyPEM, "pulumi-ui", uiIP, []string{"server"}, 24*time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, cert.CertPEM)
	assert.NotEmpty(t, cert.KeyPEM)
	assert.Contains(t, string(cert.CertPEM), "NEBULA CERTIFICATE")
}

func TestIssueAgentCert(t *testing.T) {
	ca, err := GenerateCA("test-ca", 48*time.Hour)
	require.NoError(t, err)

	subnet := "10.42.1.0/24"
	agentIP, err := AgentAddress(subnet)
	require.NoError(t, err)

	cert, err := IssueCert(ca.CertPEM, ca.KeyPEM, "agent", agentIP, []string{"agent"}, 24*time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, cert.CertPEM)
	assert.NotEmpty(t, cert.KeyPEM)
}

func TestUIAndAgentCerts_DifferentIdentities(t *testing.T) {
	ca, err := GenerateCA("test-ca", 48*time.Hour)
	require.NoError(t, err)

	subnet := "10.42.3.0/24"
	uiIP, err := UIAddress(subnet)
	require.NoError(t, err)
	agentIP, err := AgentAddress(subnet)
	require.NoError(t, err)

	uiCert, err := IssueCert(ca.CertPEM, ca.KeyPEM, "pulumi-ui", uiIP, []string{"server"}, 24*time.Hour)
	require.NoError(t, err)
	agentCert, err := IssueCert(ca.CertPEM, ca.KeyPEM, "agent", agentIP, []string{"agent"}, 24*time.Hour)
	require.NoError(t, err)

	assert.NotEqual(t, uiCert.CertPEM, agentCert.CertPEM, "UI and agent certs must differ")
	assert.NotEqual(t, uiCert.KeyPEM, agentCert.KeyPEM, "UI and agent keys must differ")
}

func TestUIAndAgentAddresses_Different(t *testing.T) {
	subnet := "10.42.7.0/24"
	uiIP, err := UIAddress(subnet)
	require.NoError(t, err)
	agentIP, err := AgentAddress(subnet)
	require.NoError(t, err)

	assert.NotEqual(t, uiIP.Addr().String(), agentIP.Addr().String())
	assert.Equal(t, "10.42.7.1", uiIP.Addr().String())
	assert.Equal(t, "10.42.7.2", agentIP.Addr().String())
}

func TestSubnetIP_OutOfRange(t *testing.T) {
	_, err := SubnetIP("invalid", 1)
	assert.Error(t, err)
}

func TestSubnetIP_ValidCases(t *testing.T) {
	tests := []struct {
		subnet    string
		hostIndex int
		wantAddr  string
	}{
		{"10.42.1.0/24", 1, "10.42.1.1"},
		{"10.42.1.0/24", 2, "10.42.1.2"},
		{"10.42.1.0/24", 254, "10.42.1.254"},
		{"10.42.0.0/24", 1, "10.42.0.1"},
		{"10.42.255.0/24", 10, "10.42.255.10"},
	}
	for _, tt := range tests {
		t.Run(tt.wantAddr, func(t *testing.T) {
			prefix, err := SubnetIP(tt.subnet, tt.hostIndex)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAddr, prefix.Addr().String())
			assert.Equal(t, 24, prefix.Bits())
		})
	}
}

func TestSubnetIP_HostIndexZero(t *testing.T) {
	prefix, err := SubnetIP("10.42.5.0/24", 0)
	require.NoError(t, err)
	assert.Equal(t, "10.42.5.0", prefix.Addr().String())
}

func TestSubnetIP_HostIndexOverflow(t *testing.T) {
	prefix, err := SubnetIP("10.42.1.0/24", 256)
	require.NoError(t, err)
	assert.Equal(t, "10.42.1.0", prefix.Addr().String(), "byte(256) wraps to 0")
}
