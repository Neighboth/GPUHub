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

// matchProviderGPUs reports whether caps satisfy the spec and which GPU fits.
// Rules (README §GPU matching):
//   - gpu_type == "any":  any GPU with VRAMGB >= min_vram_gb (or any GPU if min_vram_gb==0) and max_count >= gpu_count
//   - gpu_type specific:  a GPU with that exact canonical type, VRAMGB >= min_vram_gb, max_count >= gpu_count
//   - The lowest-VRAM fitting GPU wins (least waste).
func matchProviderGPUs(caps providers.Capabilities, spec providers.JobSpec) (providers.GPU, bool) {
	var best providers.GPU
	found := false
	for _, g := range caps.GPUs {
		if spec.GPUType != "" && !strings.EqualFold(spec.GPUType, "any") {
			if !strings.EqualFold(g.Type, spec.GPUType) {
				continue
			}
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
		if !found || g.VRAMGB < best.VRAMGB || (g.VRAMGB == best.VRAMGB && g.MaxCount >= best.MaxCount) {
			best, found = g, true
		}
	}
	return best, found
}

// rejectionReason explains, human-readable, why a provider cannot run the job.
func rejectionReason(caps providers.Capabilities, spec providers.JobSpec) string {
	if len(caps.GPUs) == 0 {
		return "provider offers no GPUs"
	}
	for _, g := range caps.GPUs {
		if !strings.EqualFold(spec.GPUType, "any") && !strings.EqualFold(g.Type, spec.GPUType) {
			continue
		}
		if spec.MinVRAMGB > 0 && g.VRAMGB < spec.MinVRAMGB {
			return fmt.Sprintf("GPU %s has %dGB VRAM < required %dGB", g.Type, g.VRAMGB, spec.MinVRAMGB)
		}
		if spec.GPUCount > 0 && g.MaxCount > 0 && g.MaxCount < spec.GPUCount {
			return fmt.Sprintf("GPU %s max %d per job < required %d", g.Type, g.MaxCount, spec.GPUCount)
		}
	}
	types := []string{}
	for _, g := range caps.GPUs {
		types = append(types, fmt.Sprintf("%s(%dGB x%d)", g.Type, g.VRAMGB, g.MaxCount))
	}
	return fmt.Sprintf("no GPU matches type=%q vram>=%dGB count=%d (offers: %s)", spec.GPUType, spec.MinVRAMGB, spec.GPUCount, strings.Join(types, ", "))
}
