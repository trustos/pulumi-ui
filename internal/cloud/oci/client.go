// Package oci provides a minimal OCI REST client with HTTP Signature authentication.
// It uses no external SDK — only stdlib crypto and net/http.
package oci

import (
	"context"
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

	"github.com/trustos/pulumi-ui/internal/cloud"
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
func (c *Client) get(ctx context.Context, rawURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
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
		return mapOCIError(resp.StatusCode, body)
	}
	if dst != nil {
		return json.Unmarshal(body, dst)
	}
	return nil
}

// mapOCIError wraps an OCI 4xx/5xx response as the matching cloud
// sentinel so callers can errors.Is without depending on the OCI
// package.
func mapOCIError(statusCode int, body []byte) error {
	var ociErr struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &ociErr)

	msg := fmt.Sprintf("OCI HTTP %d", statusCode)
	if ociErr.Message != "" {
		msg = fmt.Sprintf("OCI %s: %s", ociErr.Code, ociErr.Message)
	}
	switch statusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", msg, cloud.ErrNotFound)
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %w", msg, cloud.ErrUnauthenticated)
	case http.StatusForbidden:
		return fmt.Errorf("%s: %w", msg, cloud.ErrPermissionDenied)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s: %w", msg, cloud.ErrRateLimited)
	}
	return errors.New(msg)
}

// Shape is the wire-format OCI compute shape. Extended fields populated
// by the richer schema parser live alongside this struct; the core
// fields here are always present.
type Shape struct {
	Shape                string              `json:"shape"`
	ProcessorDescription string              `json:"processorDescription"`
	IsFlexible           bool                `json:"isFlexible,omitempty"`
	Ocpus                float64             `json:"ocpus,omitempty"`        // fixed-shape default; for flex shapes see OcpuOptions
	MemoryInGBs          float64             `json:"memoryInGBs,omitempty"`  // fixed-shape default; for flex shapes see MemoryOptions
	OcpuOptions          *ShapeOcpuOptions   `json:"ocpuOptions,omitempty"`
	MemoryOptions        *ShapeMemoryOptions `json:"memoryOptions,omitempty"`
	NetworkingBandwidth  float64             `json:"networkingBandwidthInGbps,omitempty"`
	MaxVnicAttachments   int                 `json:"maxVnicAttachments,omitempty"`
}

type ShapeOcpuOptions struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type ShapeMemoryOptions struct {
	MinInGBs            float64 `json:"minInGBs"`
	MaxInGBs            float64 `json:"maxInGBs"`
	DefaultPerOcpuInGBs float64 `json:"defaultPerOcpuInGBs,omitempty"`
	MinPerOcpuInGBs     float64 `json:"minPerOcpuInGBs,omitempty"`
	MaxPerOcpuInGBs     float64 `json:"maxPerOcpuInGBs,omitempty"`
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
func (c *Client) VerifyCredentials(ctx context.Context) error {
	return c.get(ctx, UserURL(c.region, c.userOCID), nil)
}

// VerifyViaTenanacy calls GET /tenancies/{tenancyOCID}. This requires the
// 'inspect tenancy' IAM policy and is exposed for debug/comparison purposes.
func (c *Client) VerifyViaTenanacy(ctx context.Context) error {
	return c.get(ctx, TenancyURL(c.region, c.tenancyOCID), nil)
}

// GetTenancyName fetches the human-readable tenancy name from the OCI Identity API.
// Returns an empty string if the call fails (e.g. missing 'inspect tenancy' IAM policy).
func (c *Client) GetTenancyName(ctx context.Context) string {
	var resp struct {
		Name string `json:"name"`
	}
	if err := c.get(ctx, TenancyURL(c.region, c.tenancyOCID), &resp); err != nil {
		return ""
	}
	return resp.Name
}

// ListShapes returns all compute shapes available in the account's
// region (union across ADs). Use ListShapesInAD for a single-AD query.
func (c *Client) ListShapes(ctx context.Context) ([]Shape, error) {
	var shapes []Shape
	if err := c.get(ctx, ShapesURL(c.region, c.tenancyOCID, ""), &shapes); err != nil {
		return nil, err
	}
	return shapes, nil
}

// ListShapesInAD returns compute shapes offered in a specific availability
// domain. OCI's /shapes endpoint filters per-AD when the query parameter
// is supplied, giving us the authoritative shape-per-AD mapping.
func (c *Client) ListShapesInAD(ctx context.Context, adName string) ([]Shape, error) {
	var shapes []Shape
	if err := c.get(ctx, ShapesURL(c.region, c.tenancyOCID, adName), &shapes); err != nil {
		return nil, err
	}
	return shapes, nil
}

// ListImages returns Oracle Linux and Canonical Ubuntu images compatible
// with the given compute shape, sorted newest-first within each OS group.
// Pass an empty shape to default to VM.Standard.A1.Flex (ARM) for
// back-compat with callers that haven't been updated.
func (c *Client) ListImages(ctx context.Context, shape string) ([]Image, error) {
	if shape == "" {
		shape = "VM.Standard.A1.Flex"
	}
	var combined []Image
	for _, os := range []string{"Oracle Linux", "Canonical Ubuntu"} {
		var batch []Image
		if err := c.get(ctx, ImagesURL(c.region, c.tenancyOCID, os, shape), &batch); err != nil {
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
func (c *Client) ListCompartments(ctx context.Context) ([]Compartment, error) {
	var compartments []Compartment
	if err := c.get(ctx, CompartmentsURL(c.region, c.tenancyOCID), &compartments); err != nil {
		return nil, err
	}
	root := Compartment{
		ID:          c.tenancyOCID,
		Name:        c.GetTenancyName(ctx),
		Description: "Tenancy root compartment",
	}
	if root.Name == "" {
		root.Name = "Root"
	}
	return append([]Compartment{root}, compartments...), nil
}

// ListAvailabilityDomains returns the availability domains for the account's region.
func (c *Client) ListAvailabilityDomains(ctx context.Context) ([]AvailabilityDomain, error) {
	var ads []AvailabilityDomain
	if err := c.get(ctx, AvailabilityDomainsURL(c.region, c.tenancyOCID), &ads); err != nil {
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
func (c *Client) ListInstancePoolInstances(ctx context.Context, compartmentID, poolID string) ([]InstancePoolInstance, error) {
	var instances []InstancePoolInstance
	u := fmt.Sprintf("%s/instancePools/%s/instances?compartmentId=%s",
		computeBase(c.region), url.PathEscape(poolID), url.QueryEscape(compartmentID))
	if err := c.get(ctx, u, &instances); err != nil {
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
func (c *Client) GetInstancePrivateIP(ctx context.Context, compartmentID, instanceID string) (string, error) {
	var attachments []VnicAttachment
	u := fmt.Sprintf("%s/vnicAttachments?compartmentId=%s&instanceId=%s",
		computeBase(c.region), url.QueryEscape(compartmentID), url.QueryEscape(instanceID))
	if err := c.get(ctx, u, &attachments); err != nil {
		return "", fmt.Errorf("list VNIC attachments: %w", err)
	}
	if len(attachments) == 0 {
		return "", fmt.Errorf("no VNIC attachments for instance %s", instanceID)
	}

	var vnic Vnic
	vnicURL := fmt.Sprintf("%s/vnics/%s",
		computeBase(c.region), url.PathEscape(attachments[0].VnicID))
	if err := c.get(ctx, vnicURL, &vnic); err != nil {
		return "", fmt.Errorf("get VNIC: %w", err)
	}
	return vnic.PrivateIP, nil
}
