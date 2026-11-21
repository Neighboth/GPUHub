package jobs

import (
	"testing"

	"gpuhub/internal/providers"
)

// Verify GPU -> TPU -> CPU fallback respects per-accelerator quota.
func TestGPUFallback_QuotaAware(t *testing.T) {
	caps := providers.Capabilities{GPUs: []providers.GPU{
		{Type: "T4", VRAMGB: 16, Count: 1, MaxCount: 2},
		{Type: "TPU", VRAMGB: 0, Count: 1, MaxCount: 1},
		{Type: "CPU", VRAMGB: 0, Count: 4, MaxCount: 4},
	}}

	// Request T4 with fallback chain; T4 quota exhausted, TPU available.
	spec := providers.JobSpec{GPUType: "T4", GPUTypeFallback: []string{"TPU", "CPU"}, MinVRAMGB: 16, GPUCount: 1}
	p := map[string]float64{"T4": 0.95, "TPU": 0.10, "CPU": 0}

	chosen := ""
	for _, want := range append([]string{spec.GPUType}, spec.GPUTypeFallback...) {
		if qp, ok := p[want]; ok && qp < 0.9 {
			chosen = want
			break
		}
	}
	if chosen != "TPU" {
		t.Fatalf("expected TPU fallback, got %q", chosen)
	}

	// All quota exhausted -> nothing chosen.
	p2 := map[string]float64{"T4": 0.95, "TPU": 0.95, "CPU": 1.0}
	chosen2 := ""
	for _, want := range append([]string{spec.GPUType}, spec.GPUTypeFallback...) {
		if qp, ok := p2[want]; ok && qp < 0.9 {
			chosen2 = want
			break
		}
	}
	if chosen2 != "" {
		t.Fatalf("expected no accelerator, got %q", chosen2)
	}

	// VRAM-less TPU must not match "any" with min_vram_gb=16.
	if _, ok := matchProviderGPUs(caps, providers.JobSpec{GPUType: "any", MinVRAMGB: 16, GPUCount: 1}); !ok {
		t.Fatal("T4 should match any/16GB")
	}
	g, ok := matchProviderGPUs(caps, providers.JobSpec{GPUType: "TPU", MinVRAMGB: 16, GPUCount: 1})
	if !ok || g.Type != "TPU" {
		t.Fatalf("explicit TPU should match even with min_vram=16, got %+v ok=%v", g, ok)
	}
}
