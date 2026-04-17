// oci-debug is a standalone CLI tool for testing OCI API calls.
// It exercises every endpoint the application uses and prints verbose output
// so you can verify credentials, endpoint URLs, and signing correctness
// without running the full server.
//
// Usage:
//
//	go run ./cmd/oci-debug \
//	  -tenancy   ocid1.tenancy.oc1..xxx \
//	  -user       ocid1.user.oc1..xxx \
//	  -fingerprint aa:bb:cc:... \
//	  -key        /path/to/private_key.pem \
//	  -region     eu-frankfurt-1
//
// All flags can also be provided via environment variables:
//
//	OCI_TENANCY_OCID, OCI_USER_OCID, OCI_FINGERPRINT, OCI_PRIVATE_KEY_FILE, OCI_REGION
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/trustos/pulumi-ui/internal/cloud/oci"
)

func main() {
	tenancy := flag.String("tenancy", env("OCI_TENANCY_OCID", ""), "Tenancy OCID")
	userOCID := flag.String("user", env("OCI_USER_OCID", ""), "User OCID")
	fingerprint := flag.String("fingerprint", env("OCI_FINGERPRINT", ""), "API key fingerprint")
	keyFile := flag.String("key", env("OCI_PRIVATE_KEY_FILE", ""), "Path to PEM private key file")
	keyInline := flag.String("key-inline", env("OCI_PRIVATE_KEY", ""), "PEM private key content (alternative to -key)")
	region := flag.String("region", env("OCI_REGION", ""), "OCI region identifier (e.g. eu-frankfurt-1)")
	flag.Parse()

	if *tenancy == "" || *userOCID == "" || *fingerprint == "" || *region == "" {
		fmt.Fprintln(os.Stderr, "error: -tenancy, -user, -fingerprint, and -region are required")
		flag.Usage()
		os.Exit(1)
	}

	var privateKey string
	switch {
	case *keyInline != "":
		privateKey = *keyInline
	case *keyFile != "":
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			fatalf("read key file: %v", err)
		}
		privateKey = string(b)
	default:
		fmt.Fprintln(os.Stderr, "error: provide -key (file path) or -key-inline (PEM content)")
		os.Exit(1)
	}

	fmt.Println("=== OCI Debug Tool ===")
	fmt.Printf("  Region:      %s\n", *region)
	fmt.Printf("  Tenancy:     %s\n", *tenancy)
	fmt.Printf("  User:        %s\n", *userOCID)
	fmt.Printf("  Fingerprint: %s\n\n", *fingerprint)

	client, err := oci.NewClient(*tenancy, *userOCID, *fingerprint, privateKey, *region)
	if err != nil {
		fatalf("create OCI client: %v", err)
	}

	// Print the URLs being called so endpoint patterns are visible.
	fmt.Println("=== Endpoint URLs ===")
	fmt.Printf("  Verify (user profile): %s\n", oci.UserURL(*region, *userOCID))
	fmt.Printf("  Tenancy info:          %s\n", oci.TenancyURL(*region, *tenancy))
	fmt.Printf("  Shapes:                %s\n", oci.ShapesURL(*region, *tenancy, ""))
	fmt.Printf("  Images (Oracle Linux): %s\n", oci.ImagesURL(*region, *tenancy, "Oracle Linux", ""))
	fmt.Printf("  Images (Ubuntu):       %s\n\n", oci.ImagesURL(*region, *tenancy, "Canonical Ubuntu", ""))

	ctx := context.Background()

	run("GET user profile (credential verification)", func() error {
		return client.VerifyCredentials(ctx)
	})

	run("GET tenancy info (requires inspect-tenancy policy)", func() error {
		return client.VerifyViaTenanacy(ctx)
	})

	run("GET compute shapes", func() error {
		shapes, err := client.ListShapes(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("    → %d shapes returned\n", len(shapes))
		for i, s := range shapes {
			if i >= 5 {
				fmt.Printf("    → ... (%d more)\n", len(shapes)-5)
				break
			}
			fmt.Printf("    → %s  (%s)\n", s.Shape, s.ProcessorDescription)
		}
		return nil
	})

	run("GET Oracle Linux images (VM.Standard.A1.Flex)", func() error {
		images, err := client.ListImages(ctx, "")
		if err != nil {
			return err
		}
		fmt.Printf("    → %d images returned\n", len(images))
		for _, img := range images {
			fmt.Printf("    → %s  |  %s %s\n", img.ID, img.OperatingSystem, img.OperatingSystemVersion)
		}
		return nil
	})
}

func run(label string, fn func() error) {
	fmt.Printf("--- %s ---\n", label)
	if err := fn(); err != nil {
		fmt.Printf("    FAIL: %v\n\n", err)
	} else {
		fmt.Printf("    OK\n\n")
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}
