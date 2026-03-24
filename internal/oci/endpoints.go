package oci

import (
	"fmt"
	"net/url"
)

// OCI REST API base URLs.
// All endpoints follow the pattern service.region.oraclecloud.com/version/resource.
//
// Identity API docs: https://docs.oracle.com/en-us/iaas/api/#/en/identity/20160918/
// Core (Compute) API docs: https://docs.oracle.com/en-us/iaas/api/#/en/iaas/20160918/
const (
	identityAPIVersion = "20160918"
	computeAPIVersion  = "20160918"
)

func identityBase(region string) string {
	return fmt.Sprintf("https://identity.%s.oraclecloud.com/%s", region, identityAPIVersion)
}

func computeBase(region string) string {
	return fmt.Sprintf("https://iaas.%s.oraclecloud.com/%s", region, computeAPIVersion)
}

// UserURL returns the endpoint to GET the user's own profile.
// Any authenticated user can read their own record — used for credential verification.
//   GET /users/{userId}
func UserURL(region, userOCID string) string {
	return fmt.Sprintf("%s/users/%s", identityBase(region), url.PathEscape(userOCID))
}

// TenancyURL returns the endpoint to GET tenancy information.
// Requires the user to have 'inspect tenancy' IAM policy — use UserURL for verification instead.
//   GET /tenancies/{tenancyId}
func TenancyURL(region, tenancyOCID string) string {
	return fmt.Sprintf("%s/tenancies/%s", identityBase(region), url.PathEscape(tenancyOCID))
}

// ShapesURL returns the endpoint to list compute shapes in a compartment.
//   GET /shapes?compartmentId={id}&limit=100
func ShapesURL(region, compartmentID string) string {
	return fmt.Sprintf("%s/shapes?compartmentId=%s&limit=100",
		computeBase(region), url.QueryEscape(compartmentID))
}

// ImagesURL returns the endpoint to list available platform images for VM.Standard.A1.Flex
// in the given region, filtered by OS and lifecycle state AVAILABLE (no deprecated images).
// Results are sorted newest first.
//
//	GET /images?compartmentId={id}&operatingSystem={os}&shape=VM.Standard.A1.Flex&lifecycleState=AVAILABLE&...
//
// Common OS names accepted by OCI: "Oracle Linux", "Canonical Ubuntu".
func ImagesURL(region, compartmentID, operatingSystem string) string {
	return fmt.Sprintf(
		"%s/images?compartmentId=%s&operatingSystem=%s&shape=%s&lifecycleState=AVAILABLE&sortBy=TIMECREATED&sortOrder=DESC&limit=50",
		computeBase(region),
		url.QueryEscape(compartmentID),
		url.QueryEscape(operatingSystem),
		url.QueryEscape("VM.Standard.A1.Flex"),
	)
}

// CompartmentsURL returns the endpoint to list compartments within a tenancy.
// Uses compartmentIdInSubtree=true to return the full tree and accessLevel=ACCESSIBLE
// to respect IAM policy. Only ACTIVE compartments are returned.
//
//	GET /compartments?compartmentId={id}&compartmentIdInSubtree=true&accessLevel=ACCESSIBLE&lifecycleState=ACTIVE&...
func CompartmentsURL(region, compartmentID string) string {
	return fmt.Sprintf(
		"%s/compartments?compartmentId=%s&compartmentIdInSubtree=true&accessLevel=ACCESSIBLE&lifecycleState=ACTIVE&sortBy=NAME&sortOrder=ASC&limit=200",
		identityBase(region),
		url.QueryEscape(compartmentID),
	)
}

// AvailabilityDomainsURL returns the endpoint to list availability domains for a tenancy.
// Regions typically have 1-3 ADs.
//
//	GET /availabilityDomains?compartmentId={id}
func AvailabilityDomainsURL(region, compartmentID string) string {
	return fmt.Sprintf(
		"%s/availabilityDomains?compartmentId=%s",
		identityBase(region),
		url.QueryEscape(compartmentID),
	)
}
