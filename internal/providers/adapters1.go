package providers

// Adapter constructors for providers with a RECURRING free tier (weekly/monthly/daily
// reset) AND a real programmatic submission path. Non-recurring / closed / trial-only
// providers were removed (2026-11 audit, see README).

func gpu(t string, vram, max int) GPU { return GPU{Type: t, VRAMGB: vram, Count: max, MaxCount: max} }

var (
	capT4      = []GPU{gpu("T4", 16, 1)}
	capT4P100  = []GPU{gpu("P100", 16, 1), gpu("T4", 16, 1)}
	capM4000   = []GPU{gpu("M4000", 8, 1)}
	capZeroGPU = []GPU{gpu("RTX6000Pro", 96, 1)}
)

// --- Paperspace Gradient (DigitalOcean) ---
// Auth: {"key":"..."} (x-api-key). Free M4000 notebook (6h auto-shutdown), real /jobs API.
// NOTE: free machines are often capacity-limited; kept because the quota recurs and the API is real.
func NewPaperspace() *apiAdapter {
	return &apiAdapter{
		id: "paperspace", name: "Paperspace Gradient", baseURL: "https://api.paperspace.com",
		caps: Capabilities{GPUs: capM4000, MaxDurationHours: 6,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: false,
			FreeTierNotes: "Free plan: M4000 8GB GPU notebook, 6h auto-shutdown; capacity may be unavailable"},
		auth:     tokenHeader("x-api-key"),
		validate: "/users/me",
		submit:   "/jobs",
		status:   func(id string) string { return "/jobs/" + id },
		logs:     func(id string) string { return "/jobs/" + id + "/logs" },
		cancel:   func(id string) string { return "/jobs/" + id + "/stop" },
	}
}

// --- Hugging Face Spaces (ZeroGPU) ---
// Auth: Bearer HF token. 5 GPU-min/day free (resets daily), RTX PRO 6000 Blackwell.
// NOTE: ZeroGPU attach is code-level (@spaces.GPU) - no external job-submit API;
// GPUHub tracks usage/quota and directs users to deploy a Gradio Space.
func NewHFSpaces() *apiAdapter {
	return &apiAdapter{
		id: "hf-spaces", name: "Hugging Face Spaces (ZeroGPU)", baseURL: "https://huggingface.co/api",
		caps: Capabilities{GPUs: capZeroGPU, MaxDurationHours: 1,
			Frameworks: []string{"pytorch", "tensorflow", "jax", "gradio"}, Notebooks: false, Docker: true, SSH: false,
			FreeTierNotes: "ZeroGPU: RTX PRO 6000 (48-96GB). Free: 5 GPU-min/day, resets 24h after first GPU use; Gradio SDK only"},
		auth:     bearer,
		validate: "/whoami-v2",
		submit:   "", // NOTE: ZeroGPU attaches inside the Space app, no external job API
	}
}

func init() {
	registerBuiltin(func() []Provider { return []Provider{NewPaperspace(), NewHFSpaces(), NewSaturn()} })
}
