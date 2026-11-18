package providers

// Canonical GPU table: nominal VRAM per GPU model (GB).
var GPUCatalog = map[string]int{
	"T4":         16,
	"P100":       16,
	"V100":       32,
	"A100":       80,
	"H100":       80,
	"L4":         24,
	"L40S":       48,
	"RTX4090":    24,
	"RTX6000Ada": 48,
	"MI300X":     192,
	"Gaudi2":     96,
	"TPU":        0, // allowed in "any" matching, never matches a specific MinVRAMGB demand meaningfully
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
