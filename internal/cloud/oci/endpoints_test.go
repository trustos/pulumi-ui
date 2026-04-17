package oci

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompartmentsURL(t *testing.T) {
	url := CompartmentsURL("us-phoenix-1", "ocid1.tenancy.oc1..aaa")
	assert.Contains(t, url, "identity.us-phoenix-1.oraclecloud.com")
	assert.Contains(t, url, "/20160918/compartments")
	assert.Contains(t, url, "compartmentId=ocid1.tenancy.oc1..aaa")
	assert.Contains(t, url, "compartmentIdInSubtree=true")
	assert.Contains(t, url, "accessLevel=ACCESSIBLE")
	assert.Contains(t, url, "lifecycleState=ACTIVE")
}

func TestAvailabilityDomainsURL(t *testing.T) {
	url := AvailabilityDomainsURL("eu-frankfurt-1", "ocid1.tenancy.oc1..bbb")
	assert.Contains(t, url, "identity.eu-frankfurt-1.oraclecloud.com")
	assert.Contains(t, url, "/20160918/availabilityDomains")
	assert.Contains(t, url, "compartmentId=ocid1.tenancy.oc1..bbb")
}

func TestShapesURL(t *testing.T) {
	url := ShapesURL("us-ashburn-1", "ocid1.tenancy.oc1..ccc")
	assert.Contains(t, url, "iaas.us-ashburn-1.oraclecloud.com")
	assert.Contains(t, url, "/20160918/shapes")
	assert.Contains(t, url, "compartmentId=ocid1.tenancy.oc1..ccc")
}

func TestImagesURL(t *testing.T) {
	url := ImagesURL("us-ashburn-1", "ocid1.tenancy.oc1..ccc", "Canonical Ubuntu", "")
	assert.Contains(t, url, "iaas.us-ashburn-1.oraclecloud.com")
	assert.Contains(t, url, "/20160918/images")
	assert.Contains(t, url, "compartmentId=ocid1.tenancy.oc1..ccc")
	assert.Contains(t, url, "operatingSystem=Canonical+Ubuntu")
	assert.Contains(t, url, "shape=VM.Standard.A1.Flex")
}

func TestImagesURL_ShapeOverride(t *testing.T) {
	url := ImagesURL("us-ashburn-1", "ocid1.tenancy.oc1..ccc", "Canonical Ubuntu", "VM.Standard.E2.1.Micro")
	assert.Contains(t, url, "shape=VM.Standard.E2.1.Micro")
}

func TestUserURL(t *testing.T) {
	url := UserURL("us-phoenix-1", "ocid1.user.oc1..xyz")
	assert.Contains(t, url, "identity.us-phoenix-1.oraclecloud.com")
	assert.Contains(t, url, "/20160918/users/ocid1.user.oc1..xyz")
}

func TestTenancyURL(t *testing.T) {
	url := TenancyURL("eu-frankfurt-1", "ocid1.tenancy.oc1..abc")
	assert.Contains(t, url, "identity.eu-frankfurt-1.oraclecloud.com")
	assert.Contains(t, url, "/20160918/tenancies/ocid1.tenancy.oc1..abc")
}
