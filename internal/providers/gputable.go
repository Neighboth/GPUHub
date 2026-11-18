package providers

// Canonical GPU table: nominal VRAM per GPU model (GB). Updated 2026-11 (research pass).
var GPUCatalog = map[string]int{
	"T4":         16,
	"P100":       16,
	"V100":       32,
	"A100":       80, // catalog keeps the larger variant; 40GB A100 is matched when requested via min_vram_gb<=40
	"A10":        24,
	"A10G":       24,
	"H100":       80,
	"H200":       141,
	"L4":         24,
	"L40S":       48,
	"RTX4090":    24,
	"RTX6000Ada": 48,
	"RTX6000Pro": 96, // RTX PRO 6000 Blackwell - HF ZeroGPU backing hardware (48GB large / 96GB xlarge)
	"M4000":      8,  // legacy Maxwell - Paperspace/Saturn free class
	"P4000":      8,
	"MI300X":     192,
	"B200":       180,
	"B300":       288,
	"Gaudi2":     96,
	"TPU":        0, // matches "any" queries only
}

// KnownGPUTypes lists gpu_type values accepted by the API (plus "any").
func KnownGPUTypes() []string {
	out := []string{"any"}
	for k := range GPUCatalog {
		out = append(out, k)
	}
	return out
}

func GPUTypeValid(t string) bool {
	if t == "any" {
		return true
	}
	_, ok := GPUCatalog[t]
	return ok
}
