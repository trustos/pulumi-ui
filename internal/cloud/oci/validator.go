package oci

import (
	"context"
	"fmt"

	"github.com/trustos/pulumi-ui/internal/cloud"
)

const instanceResourceType = "oci:Core/instance:Instance"

// Validate walks the rendered resource graph for OCI compute instances
// and cross-checks shape/shapeConfig/image consistency against live
// OCI metadata. Runs at stack-config submit time; returns an empty
// slice if everything is consistent or no relevant resources exist.
func (p *Provider) Validate(ctx context.Context, graph cloud.ResourceGraph, ref cloud.AccountRef) []cloud.ValidationError {
	instances := collectInstances(graph)
	if len(instances) == 0 {
		return nil
	}

	types, err := p.ListComputeTypes(ctx, ref.Region)
	if err != nil {
		// A metadata lookup failure must not block submission; structural
		// validation already passed. Signal to the caller via a Level-8
		// warning they can surface, but do not treat as fatal.
		return []cloud.ValidationError{{
			Level:   cloud.LevelRuntimeCompat,
			Message: fmt.Sprintf("unable to verify shape/image compatibility: %s", err),
		}}
	}
	typeByName := map[string]cloud.ComputeType{}
	for _, t := range types {
		typeByName[t.Name] = t
	}

	var errs []cloud.ValidationError
	imageListCache := map[string][]cloud.Image{}

	for _, inst := range instances {
		shape, _ := inst.Properties["shape"].(string)
		if shape == "" {
			continue
		}
		ct, known := typeByName[shape]
		if !known {
			errs = append(errs, cloud.ValidationError{
				Level:   cloud.LevelRuntimeCompat,
				Field:   fmt.Sprintf("resources.%s.properties.shape", inst.Name),
				Message: fmt.Sprintf("shape %q is not available in this OCI account/region", shape),
			})
			continue
		}

		errs = append(errs, checkShapeConfig(inst, ct)...)
		errs = append(errs, checkAvailabilityDomain(inst, ct)...)
		errs = append(errs, p.checkImage(ctx, ref.Region, inst, shape, imageListCache)...)
	}
	return errs
}

// checkAvailabilityDomain verifies the instance's chosen AD is one where
// the selected shape is actually offered.
//
// Skip rules:
//   - Shape metadata has no AvailabilityDomains (unknown availability) →
//     skip; we don't want to block on incomplete metadata.
//   - AD property is a Pulumi expression (unresolved ${...} reference) →
//     skip; those resolve at apply time. This is the back-compat path for
//     blueprints still using `${availabilityDomains[N].name}`.
//
// An explicit empty string is a user error (tag selector deselected every
// AD; hand-edited to blank) and is rejected with a clear message so the
// user sees the problem at config submit rather than mid-deploy.
func checkAvailabilityDomain(inst cloud.ResourceNode, ct cloud.ComputeType) []cloud.ValidationError {
	if len(ct.AvailabilityDomains) == 0 {
		return nil
	}
	ad, _ := inst.Properties["availabilityDomain"].(string)
	// Unresolved Pulumi reference — skip (this is a render-time expression).
	if len(ad) > 1 && ad[0] == '$' {
		return nil
	}
	if ad == "" {
		return []cloud.ValidationError{{
			Level:   cloud.LevelRuntimeCompat,
			Field:   fmt.Sprintf("resources.%s.properties.availabilityDomain", inst.Name),
			Message: "no availability domain selected — pick at least one",
		}}
	}
	for _, allowed := range ct.AvailabilityDomains {
		if allowed == ad {
			return nil
		}
	}
	suggestion := ""
	if len(ct.AvailabilityDomains) > 0 {
		suggestion = fmt.Sprintf(" — try %q", ct.AvailabilityDomains[0])
	}
	return []cloud.ValidationError{{
		Level:   cloud.LevelRuntimeCompat,
		Field:   fmt.Sprintf("resources.%s.properties.availabilityDomain", inst.Name),
		Message: fmt.Sprintf("shape %q is not available in AD %q%s", ct.Name, ad, suggestion),
	}}
}

func collectInstances(graph cloud.ResourceGraph) []cloud.ResourceNode {
	var out []cloud.ResourceNode
	for _, n := range graph.Resources {
		if n.Type == instanceResourceType {
			out = append(out, n)
		}
	}
	return out
}

func checkShapeConfig(inst cloud.ResourceNode, ct cloud.ComputeType) []cloud.ValidationError {
	sc, present := inst.Properties["shapeConfig"]
	switch sizing := ct.Sizing.(type) {
	case cloud.FixedSizing:
		if present && sc != nil {
			return []cloud.ValidationError{{
				Level:   cloud.LevelRuntimeCompat,
				Field:   fmt.Sprintf("resources.%s.properties.shapeConfig", inst.Name),
				Message: fmt.Sprintf("shape %q is fixed; remove shapeConfig (OCI will reject it)", ct.Name),
			}}
		}
	case cloud.RangeSizing:
		if !present || sc == nil {
			return nil
		}
		cfg, _ := sc.(map[string]any)
		if cfg == nil {
			return nil
		}
		var errs []cloud.ValidationError
		if v, ok := toFloat(cfg["ocpus"]); ok {
			if sizing.VCPURange.Min > 0 && v < sizing.VCPURange.Min {
				errs = append(errs, cloud.ValidationError{
					Level:   cloud.LevelRuntimeCompat,
					Field:   fmt.Sprintf("resources.%s.properties.shapeConfig.ocpus", inst.Name),
					Message: fmt.Sprintf("ocpus %g is below shape %q minimum of %g", v, ct.Name, sizing.VCPURange.Min),
				})
			}
			if sizing.VCPURange.Max > 0 && v > sizing.VCPURange.Max {
				errs = append(errs, cloud.ValidationError{
					Level:   cloud.LevelRuntimeCompat,
					Field:   fmt.Sprintf("resources.%s.properties.shapeConfig.ocpus", inst.Name),
					Message: fmt.Sprintf("ocpus %g exceeds shape %q maximum of %g", v, ct.Name, sizing.VCPURange.Max),
				})
			}
		}
		if v, ok := toFloat(cfg["memoryInGbs"]); ok {
			if sizing.MemGiBRange.Min > 0 && v < sizing.MemGiBRange.Min {
				errs = append(errs, cloud.ValidationError{
					Level:   cloud.LevelRuntimeCompat,
					Field:   fmt.Sprintf("resources.%s.properties.shapeConfig.memoryInGbs", inst.Name),
					Message: fmt.Sprintf("memoryInGbs %g is below shape %q minimum of %g", v, ct.Name, sizing.MemGiBRange.Min),
				})
			}
			if sizing.MemGiBRange.Max > 0 && v > sizing.MemGiBRange.Max {
				errs = append(errs, cloud.ValidationError{
					Level:   cloud.LevelRuntimeCompat,
					Field:   fmt.Sprintf("resources.%s.properties.shapeConfig.memoryInGbs", inst.Name),
					Message: fmt.Sprintf("memoryInGbs %g exceeds shape %q maximum of %g", v, ct.Name, sizing.MemGiBRange.Max),
				})
			}
		}
		return errs
	}
	return nil
}

func (p *Provider) checkImage(ctx context.Context, region string, inst cloud.ResourceNode, shape string, cache map[string][]cloud.Image) []cloud.ValidationError {
	src, _ := inst.Properties["sourceDetails"].(map[string]any)
	if src == nil {
		return nil
	}
	if st, _ := src["sourceType"].(string); st != "image" {
		return nil
	}
	imageID, _ := src["sourceId"].(string)
	if imageID == "" {
		return nil
	}
	imgs, cached := cache[shape]
	if !cached {
		fetched, err := p.ListImages(ctx, region, shape)
		if err != nil {
			return nil // network failure; don't block submission on this
		}
		cache[shape] = fetched
		imgs = fetched
	}
	for _, im := range imgs {
		if im.ID == imageID {
			return nil
		}
	}
	return []cloud.ValidationError{{
		Level:   cloud.LevelRuntimeCompat,
		Field:   fmt.Sprintf("resources.%s.properties.sourceDetails.sourceId", inst.Name),
		Message: fmt.Sprintf("image %s is not compatible with shape %q", imageID, shape),
	}}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		var f float64
		_, err := fmt.Sscanf(x, "%g", &f)
		return f, err == nil
	}
	return 0, false
}
