package jobs

import (
	"fmt"
	"strings"

	"gpuhub/internal/providers"
)

// PlannedProvider is a provider that survived the GPU-type/VRAM/count filter for a job.
type PlannedProvider struct {
	ProviderID string
	GPU        providers.GPU // the cheapest fitting GPU offered
}

// matchProviderGPUs reports whether caps satisfy the spec and which GPU fits best.
// Tries spec.GPUType first, then spec.GPUTypeFallback in declared order.
// The lowest-VRAM fitting GPU of the HIGHEST-priority requested type wins.
func matchProviderGPUs(caps providers.Capabilities, spec providers.JobSpec) (providers.GPU, bool) {
	wants := []string{spec.GPUType}
	if !strings.EqualFold(spec.GPUType, "any") {
		wants = append(wants, spec.GPUTypeFallback...)
	}
	for _, want := range wants {
		var best providers.GPU
		found := false
		for _, g := range caps.GPUs {
			if !strings.EqualFold(want, "any") && !strings.EqualFold(g.Type, want) {
				continue
			}
			if spec.MinVRAMGB > 0 && g.VRAMGB < spec.MinVRAMGB {
				continue
			}
			if spec.GPUCount > 0 && g.MaxCount > 0 && g.MaxCount < spec.GPUCount {
				continue
			}
			if spec.GPUCount > 1 && g.MaxCount == 0 {
				continue
			}
			if !found || g.VRAMGB < best.VRAMGB {
				best, found = g, true
			}
		}
		if found {
			return best, true
		}
	}
	return providers.GPU{}, false
}

// rejectionReason explains, human-readable, why a provider cannot run the job.
func rejectionReason(caps providers.Capabilities, spec providers.JobSpec) string {
	if len(caps.GPUs) == 0 {
		return "provider offers no GPUs"
	}
	wants := []string{spec.GPUType}
	if !strings.EqualFold(spec.GPUType, "any") {
		wants = append(wants, spec.GPUTypeFallback...)
	}
	for _, want := range wants {
		for _, g := range caps.GPUs {
			if strings.EqualFold(want, "any") || strings.EqualFold(g.Type, want) {
				if spec.MinVRAMGB > 0 && g.VRAMGB < spec.MinVRAMGB {
					return fmt.Sprintf("GPU %s has %dGB VRAM < required %dGB", g.Type, g.VRAMGB, spec.MinVRAMGB)
				}
				if spec.GPUCount > 0 && g.MaxCount > 0 && g.MaxCount < spec.GPUCount {
					return fmt.Sprintf("GPU %s max %d per job < required %d", g.Type, g.MaxCount, spec.GPUCount)
				}
				return "" // shouldn't happen - matched
			}
		}
	}
	types := []string{}
	for _, g := range caps.GPUs {
		types = append(types, fmt.Sprintf("%s(%dGB x%d)", g.Type, g.VRAMGB, g.MaxCount))
	}
	return fmt.Sprintf("no GPU matches type=(%s) vram>=%dGB count=%d (offers: %s)", strings.Join(wants, ","), spec.MinVRAMGB, spec.GPUCount, strings.Join(types, ", "))
}
