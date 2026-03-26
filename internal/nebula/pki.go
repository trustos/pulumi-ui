package nebula

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/slackhq/nebula/cert"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/ed25519"
)

// CertBundle holds a PEM-encoded certificate and its corresponding private key.
type CertBundle struct {
	CertPEM []byte
	KeyPEM  []byte
}

// GenerateCA creates a new Nebula Certificate Authority valid for the given duration.
// Uses ed25519 for the signing key (Curve25519).
func GenerateCA(name string, duration time.Duration) (*CertBundle, error) {
	pub, rawPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	t := &cert.TBSCertificate{
		Version:   cert.Version1,
		Name:      name,
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(duration),
		PublicKey: pub,
		IsCA:      true,
		Curve:     cert.Curve_CURVE25519,
	}

	c, err := t.Sign(nil, cert.Curve_CURVE25519, rawPriv)
	if err != nil {
		return nil, fmt.Errorf("sign CA cert: %w", err)
	}

	certPEM, err := c.MarshalPEM()
	if err != nil {
		return nil, fmt.Errorf("marshal CA cert PEM: %w", err)
	}

	keyPEM := cert.MarshalSigningPrivateKeyToPEM(cert.Curve_CURVE25519, rawPriv)

	return &CertBundle{CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

// IssueCert generates a new node certificate signed by the given CA.
// The node gets an x25519 keypair for Noise Protocol key exchange.
func IssueCert(caCertPEM, caKeyPEM []byte, name string, ip netip.Prefix, groups []string, duration time.Duration) (*CertBundle, error) {
	caCert, _, err := cert.UnmarshalCertificateFromPEM(caCertPEM)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}

	caKey, _, _, err := cert.UnmarshalSigningPrivateKeyFromPEM(caKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse CA key: %w", err)
	}

	pub, rawPriv := x25519Keypair()

	t := &cert.TBSCertificate{
		Version:   cert.Version1,
		Name:      name,
		Networks:  []netip.Prefix{ip},
		Groups:    groups,
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(duration),
		PublicKey: pub,
		IsCA:      false,
		Curve:     cert.Curve_CURVE25519,
	}

	c, err := t.Sign(caCert, cert.Curve_CURVE25519, caKey)
	if err != nil {
		return nil, fmt.Errorf("sign node cert: %w", err)
	}

	certPEM, err := c.MarshalPEM()
	if err != nil {
		return nil, fmt.Errorf("marshal node cert PEM: %w", err)
	}

	keyPEM := cert.MarshalPrivateKeyToPEM(cert.Curve_CURVE25519, rawPriv)

	return &CertBundle{CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

// GenerateNodeCerts issues n node certificates signed by the given CA.
// Nodes are assigned consecutive Nebula IPs starting at .2 within the /24 subnet
// (node 0 → subnet.2, node 1 → subnet.3, …, node n-1 → subnet.n+1).
//
// Returns the CertBundles and the corresponding Nebula IP strings (e.g. "10.42.1.2/24").
func GenerateNodeCerts(caCertPEM, caKeyPEM []byte, subnet string, n int, duration time.Duration) ([]*CertBundle, []string, error) {
	bundles := make([]*CertBundle, n)
	ips := make([]string, n)
	for i := 0; i < n; i++ {
		ip, err := SubnetIP(subnet, i+2) // .2 for node 0, .3 for node 1, …
		if err != nil {
			return nil, nil, fmt.Errorf("compute IP for node %d: %w", i, err)
		}
		cert, err := IssueCert(caCertPEM, caKeyPEM, fmt.Sprintf("node-%d", i), ip, []string{"agent"}, duration)
		if err != nil {
			return nil, nil, fmt.Errorf("issue cert for node %d: %w", i, err)
		}
		bundles[i] = cert
		ips[i] = ip.String()
	}
	return bundles, ips, nil
}

func x25519Keypair() (pub, priv []byte) {
	priv = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, priv); err != nil {
		panic(err)
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		panic(err)
	}
	return pub, priv
}
