package blueprints_test

import (
	"os"
	"testing"

	"github.com/trustos/pulumi-ui/internal/blueprints"
)

func TestForkNomadClusterRoundTrip(t *testing.T) {
	body, err := os.ReadFile("../../blueprints/nomad-cluster.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	yamlBody := string(body)

	// Simulate what the frontend does: parse to graph, serialize back, validate
	// But we can't run the TS parser here. Instead, validate the raw YAML.
	errs := blueprints.ValidateBlueprint(yamlBody)
	for _, e := range errs {
		t.Logf("L%d [level %d] %s (field=%s)", e.Line, e.Level, e.Message, e.Field)
	}

	// Also try rendering to see what the rendered YAML looks like
	fields, _, _ := blueprints.ParseConfigFields(yamlBody)
	cfg := map[string]string{}
	for _, f := range fields {
		if f.Default != "" {
			cfg[f.Key] = f.Default
		} else {
			cfg[f.Key] = "placeholder-" + f.Key
		}
	}
	rendered, err := blueprints.RenderTemplate(yamlBody, cfg)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}

	lines := 0
	for range rendered {
		if rendered[lines] == '\n' {
			lines++
		}
		if lines >= len(rendered)-1 {
			break
		}
	}
	t.Logf("Rendered YAML: %d bytes", len(rendered))

	// Check around line 364
	renderedLines := splitLines(rendered)
	t.Logf("Rendered YAML: %d lines", len(renderedLines))
	if len(renderedLines) >= 364 {
		start := 360
		if start < 0 { start = 0 }
		end := 368
		if end > len(renderedLines) { end = len(renderedLines) }
		for i := start; i < end; i++ {
			t.Logf("  line %d: %s", i+1, renderedLines[i])
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
