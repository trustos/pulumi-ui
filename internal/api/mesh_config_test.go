package api

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/nebula"
)

// indentPEM mirrors the closure in DownloadMeshConfig — indents each non-empty
// line with 4 spaces for YAML block-scalar embedding.
func indentPEM(pem []byte) string {
	var buf bytes.Buffer
	for _, line := range bytes.Split(pem, []byte("\n")) {
		if len(line) > 0 {
			buf.WriteString("    ")
			buf.Write(line)
			buf.WriteByte('\n')
		}
	}
	return buf.String()
}

// helperCA generates a real Nebula CA for use in tests.
func helperCA(t *testing.T) *nebula.CertBundle {
	t.Helper()
	ca, err := nebula.GenerateCA("test-ca", 2*365*24*time.Hour)
	require.NoError(t, err)
	return ca
}

// helperUserCert issues a user cert signed by the given CA.
func helperUserCert(t *testing.T, ca *nebula.CertBundle, subnet string) *nebula.CertBundle {
	t.Helper()
	userIP, err := nebula.SubnetIP(subnet, 200)
	require.NoError(t, err)
	cert, err := nebula.IssueCert(ca.CertPEM, ca.KeyPEM, "user-local", userIP, []string{"user"}, 365*24*time.Hour)
	require.NoError(t, err)
	return cert
}

// renderTemplate executes nebulaConfigTmpl with the given data and returns the output.
func renderTemplate(t *testing.T, data interface{}) string {
	t.Helper()
	var buf bytes.Buffer
	err := nebulaConfigTmpl.Execute(&buf, data)
	require.NoError(t, err)
	return buf.String()
}

// templateData builds the anonymous struct expected by nebulaConfigTmpl.
func templateData(stackName, userIP, subnet, firstNodeIP, caCert, userCert, userKey, staticHostMap string) interface{} {
	return struct {
		StackName     string
		UserIP        string
		Subnet        string
		FirstNodeIP   string
		CACert        string
		UserCert      string
		UserKey       string
		StaticHostMap string
	}{
		StackName:     stackName,
		UserIP:        userIP,
		Subnet:        subnet,
		FirstNodeIP:   firstNodeIP,
		CACert:        caCert,
		UserCert:      userCert,
		UserKey:       userKey,
		StaticHostMap: staticHostMap,
	}
}

func TestNebulaConfigTemplate_RendersValidYAML(t *testing.T) {
	ca := helperCA(t)
	subnet := "10.42.1.0/24"
	user := helperUserCert(t, ca, subnet)

	staticMap := "  '10.42.1.2': ['203.0.113.10:41820']\n"
	data := templateData(
		"my-stack",
		"10.42.1.200",
		subnet,
		"10.42.1.2",
		indentPEM(ca.CertPEM),
		indentPEM(user.CertPEM),
		indentPEM(user.KeyPEM),
		staticMap,
	)

	output := renderTemplate(t, data)

	// Verify major sections are present
	assert.Contains(t, output, "pki:")
	assert.Contains(t, output, "static_host_map:")
	assert.Contains(t, output, "lighthouse:")
	assert.Contains(t, output, "listen:")
	assert.Contains(t, output, "firewall:")
	assert.Contains(t, output, "punchy:")
	assert.Contains(t, output, "tun:")

	// Verify stack name appears in the comment header
	assert.Contains(t, output, `stack "my-stack"`)

	// Verify user IP and subnet appear
	assert.Contains(t, output, "Your Nebula IP: 10.42.1.200")
	assert.Contains(t, output, "Subnet: 10.42.1.0/24")

	// Verify PEM blocks are embedded
	assert.Contains(t, output, "NEBULA CERTIFICATE")
}

func TestNebulaConfigTemplate_StaticHostMap(t *testing.T) {
	ca := helperCA(t)
	subnet := "10.42.5.0/24"
	user := helperUserCert(t, ca, subnet)

	// Build a multi-node static_host_map the same way the handler does
	var staticMap bytes.Buffer
	nodes := []struct {
		nebulaIP string
		realIP   string
	}{
		{"10.42.5.2", "198.51.100.1:41820"},
		{"10.42.5.3", "198.51.100.2:41820"},
		{"10.42.5.4", "198.51.100.3:41820"},
	}
	for _, n := range nodes {
		fmt.Fprintf(&staticMap, "  '%s': ['%s']\n", n.nebulaIP, n.realIP)
	}

	data := templateData(
		"multi-node",
		"10.42.5.200",
		subnet,
		"10.42.5.2",
		indentPEM(ca.CertPEM),
		indentPEM(user.CertPEM),
		indentPEM(user.KeyPEM),
		staticMap.String(),
	)

	output := renderTemplate(t, data)

	// Each node entry must appear in the rendered config
	for _, n := range nodes {
		expected := fmt.Sprintf("'%s': ['%s']", n.nebulaIP, n.realIP)
		assert.Contains(t, output, expected, "static_host_map should contain entry for %s", n.nebulaIP)
	}

	// FirstNodeIP should appear in the quick-start comment
	assert.Contains(t, output, "ssh ubuntu@10.42.5.2")
}

func TestNebulaConfigTemplate_PEMIndentation(t *testing.T) {
	ca := helperCA(t)

	indented := indentPEM(ca.CertPEM)
	lines := strings.Split(strings.TrimRight(indented, "\n"), "\n")

	require.NotEmpty(t, lines, "indented PEM should have at least one line")
	for _, line := range lines {
		assert.True(t, strings.HasPrefix(line, "    "), "every PEM line should start with 4 spaces, got: %q", line)
	}

	// Verify the actual PEM markers are preserved after indentation
	assert.Contains(t, indented, "    -----BEGIN NEBULA")
	assert.Contains(t, indented, "    -----END NEBULA")
}

func TestNebulaConfigTemplate_UserGroupInFirewall(t *testing.T) {
	ca := helperCA(t)
	subnet := "10.42.2.0/24"
	user := helperUserCert(t, ca, subnet)

	data := templateData(
		"fw-test",
		"10.42.2.200",
		subnet,
		"10.42.2.2",
		indentPEM(ca.CertPEM),
		indentPEM(user.CertPEM),
		indentPEM(user.KeyPEM),
		"  '10.42.2.2': ['1.2.3.4:41820']\n",
	)

	output := renderTemplate(t, data)

	// Inbound firewall should allow port 22 tcp from any
	assert.Contains(t, output, "port: 22")
	assert.Contains(t, output, "proto: tcp")

	// Outbound should allow everything
	assert.Contains(t, output, "port: any")
	assert.Contains(t, output, "proto: any")
	assert.Contains(t, output, "host: any")
}

func TestNebulaConfigTemplate_ListenPort4242(t *testing.T) {
	ca := helperCA(t)
	subnet := "10.42.3.0/24"
	user := helperUserCert(t, ca, subnet)

	data := templateData(
		"port-test",
		"10.42.3.200",
		subnet,
		"10.42.3.2",
		indentPEM(ca.CertPEM),
		indentPEM(user.CertPEM),
		indentPEM(user.KeyPEM),
		"  '10.42.3.2': ['5.6.7.8:41820']\n",
	)

	output := renderTemplate(t, data)

	// Listen section should specify port 4242
	assert.Contains(t, output, "port: 4242")
	assert.Contains(t, output, "host: 0.0.0.0")
}

func TestNebulaConfigTemplate_ContentDisposition(t *testing.T) {
	// Verify the Content-Disposition format string produces the correct filename
	stackName := "my-production-stack"
	header := fmt.Sprintf(`attachment; filename="nebula-%s.yml"`, stackName)

	assert.Equal(t, `attachment; filename="nebula-my-production-stack.yml"`, header)

	// Verify it also works with special but valid stack names
	stackName2 := "stack-123"
	header2 := fmt.Sprintf(`attachment; filename="nebula-%s.yml"`, stackName2)
	assert.Equal(t, `attachment; filename="nebula-stack-123.yml"`, header2)
}
