// Package oci provides a minimal OCI REST client with HTTP Signature authentication.
// It uses no external SDK — only stdlib crypto and net/http.
package oci

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a minimal OCI REST API client that handles HTTP Signature auth (no SDK needed).
type Client struct {
	tenancyOCID string
	userOCID    string
	fingerprint string
	privateKey  *rsa.PrivateKey
	region      string
	http        *http.Client
}

// NewClient parses the PEM private key and returns a ready-to-use Client.
func NewClient(tenancyOCID, userOCID, fingerprint, privateKeyPEM, region string) (*Client, error) {
	key, err := parseRSAKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return &Client{
		tenancyOCID: tenancyOCID,
		userOCID:    userOCID,
		fingerprint: fingerprint,
		privateKey:  key,
		region:      region,
		http:        &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func parseRSAKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block found in private key")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("PKCS8 key is not RSA")
		}
		return rk, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}

// signRequest adds the OCI HTTP Signature Authorization header to a GET request.
// Signing covers: (request-target), date, host.
func (c *Client) signRequest(req *http.Request) error {
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)

	requestTarget := strings.ToLower(req.Method) + " " + req.URL.RequestURI()
	host := req.URL.Host

	signingString := "(request-target): " + requestTarget + "\n" +
		"date: " + date + "\n" +
		"host: " + host

	sum := sha256.Sum256([]byte(signingString))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	keyID := c.tenancyOCID + "/" + c.userOCID + "/" + c.fingerprint
	req.Header.Set("Authorization", fmt.Sprintf(
		`Signature version="1",keyId="%s",algorithm="rsa-sha256",headers="(request-target) date host",signature="%s"`,
		keyID, base64.StdEncoding.EncodeToString(sig),
	))
	return nil
}

// get performs a signed GET request and unmarshals the JSON response body into dst.
// Pass dst=nil to just check for a 2xx status.
func (c *Client) get(rawURL string, dst any) error {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return err
	}
	if err := c.signRequest(req); err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("OCI request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// Try to extract OCI error message
		var ociErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &ociErr) == nil && ociErr.Message != "" {
			return fmt.Errorf("OCI %s: %s", ociErr.Code, ociErr.Message)
		}
		return fmt.Errorf("OCI API returned HTTP %d", resp.StatusCode)
	}
	if dst != nil {
		return json.Unmarshal(body, dst)
	}
	return nil
}

// Shape is a simplified OCI compute shape.
type Shape struct {
	Shape                string `json:"shape"`
	ProcessorDescription string `json:"processorDescription"`
}

// Image is a simplified OCI platform image.
type Image struct {
	ID                     string `json:"id"`
	DisplayName            string `json:"displayName"`
	OperatingSystem        string `json:"operatingSystem"`
	OperatingSystemVersion string `json:"operatingSystemVersion"`
}

// VerifyCredentials calls GET /users/{userOCID} on the OCI Identity API.
// Any authenticated user can always read their own profile, making this
// the most reliable way to confirm that key + fingerprint + OCIDs are valid.
func (c *Client) VerifyCredentials() error {
	return c.get(UserURL(c.region, c.userOCID), nil)
}

// VerifyViaTenanacy calls GET /tenancies/{tenancyOCID}. This requires the
// 'inspect tenancy' IAM policy and is exposed for debug/comparison purposes.
func (c *Client) VerifyViaTenanacy() error {
	return c.get(TenancyURL(c.region, c.tenancyOCID), nil)
}

// GetTenancyName fetches the human-readable tenancy name from the OCI Identity API.
// Returns an empty string if the call fails (e.g. missing 'inspect tenancy' IAM policy).
func (c *Client) GetTenancyName() string {
	var resp struct {
		Name string `json:"name"`
	}
	if err := c.get(TenancyURL(c.region, c.tenancyOCID), &resp); err != nil {
		return ""
	}
	return resp.Name
}

// ListShapes returns all compute shapes available in the account's region.
func (c *Client) ListShapes() ([]Shape, error) {
	var shapes []Shape
	if err := c.get(ShapesURL(c.region, c.tenancyOCID), &shapes); err != nil {
		return nil, err
	}
	return shapes, nil
}

// ListImages returns Oracle Linux and Canonical Ubuntu images compatible with
// VM.Standard.A1.Flex (ARM), sorted newest-first within each OS group.
func (c *Client) ListImages() ([]Image, error) {
	var combined []Image
	for _, os := range []string{"Oracle Linux", "Canonical Ubuntu"} {
		var batch []Image
		if err := c.get(ImagesURL(c.region, c.tenancyOCID, os), &batch); err != nil {
			return nil, fmt.Errorf("%s images: %w", os, err)
		}
		combined = append(combined, batch...)
	}
	return combined, nil
}

// Compartment is a simplified OCI IAM compartment.
type Compartment struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	CompartmentID string `json:"compartmentId"`
}

// AvailabilityDomain is an OCI availability domain within a region.
type AvailabilityDomain struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// ListCompartments returns all active compartments accessible to the user.
// The tenancy root compartment is prepended as the first entry since the
// Identity API only returns child compartments.
func (c *Client) ListCompartments() ([]Compartment, error) {
	var compartments []Compartment
	if err := c.get(CompartmentsURL(c.region, c.tenancyOCID), &compartments); err != nil {
		return nil, err
	}
	root := Compartment{
		ID:          c.tenancyOCID,
		Name:        c.GetTenancyName(),
		Description: "Tenancy root compartment",
	}
	if root.Name == "" {
		root.Name = "Root"
	}
	return append([]Compartment{root}, compartments...), nil
}

// ListAvailabilityDomains returns the availability domains for the account's region.
func (c *Client) ListAvailabilityDomains() ([]AvailabilityDomain, error) {
	var ads []AvailabilityDomain
	if err := c.get(AvailabilityDomainsURL(c.region, c.tenancyOCID), &ads); err != nil {
		return nil, err
	}
	return ads, nil
}

// InstancePoolInstance represents a member instance of an InstancePool.
// OCI REST API returns InstanceSummary objects from ListInstancePoolInstances.
type InstancePoolInstance struct {
	ID             string `json:"id"`
	State          string `json:"state"`
	LifecycleState string `json:"lifecycleState"`
	DisplayName    string `json:"displayName"`
}

// ListInstancePoolInstances returns all instances in the given pool.
func (c *Client) ListInstancePoolInstances(compartmentID, poolID string) ([]InstancePoolInstance, error) {
	var instances []InstancePoolInstance
	u := fmt.Sprintf("%s/instancePools/%s/instances?compartmentId=%s",
		computeBase(c.region), url.PathEscape(poolID), url.QueryEscape(compartmentID))
	if err := c.get(u, &instances); err != nil {
		return nil, err
	}
	return instances, nil
}

// InstanceDetail contains the fields we need from GET /instances/{id}.
type InstanceDetail struct {
	ID        string `json:"id"`
	PrivateIP string `json:"-"` // resolved separately via VNIC
}

// VnicAttachment represents a VNIC attachment for an instance.
type VnicAttachment struct {
	VnicID string `json:"vnicId"`
}

// Vnic represents a VNIC with its private IP.
type Vnic struct {
	PrivateIP string `json:"privateIp"`
	PublicIP  string `json:"publicIp"`
}

// GetInstancePrivateIP resolves the private IP of a compute instance via its VNIC.
func (c *Client) GetInstancePrivateIP(compartmentID, instanceID string) (string, error) {
	// List VNIC attachments for the instance
	var attachments []VnicAttachment
	u := fmt.Sprintf("%s/vnicAttachments?compartmentId=%s&instanceId=%s",
		computeBase(c.region), url.QueryEscape(compartmentID), url.QueryEscape(instanceID))
	if err := c.get(u, &attachments); err != nil {
		return "", fmt.Errorf("list VNIC attachments: %w", err)
	}
	if len(attachments) == 0 {
		return "", fmt.Errorf("no VNIC attachments for instance %s", instanceID)
	}

	// Get the VNIC details
	var vnic Vnic
	vnicURL := fmt.Sprintf("%s/vnics/%s",
		computeBase(c.region), url.PathEscape(attachments[0].VnicID))
	if err := c.get(vnicURL, &vnic); err != nil {
		return "", fmt.Errorf("get VNIC: %w", err)
	}
	return vnic.PrivateIP, nil
}
