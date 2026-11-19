package providers

// Part 2: Beam, Intel Tiber, Genesis, Anyscale, JarvisLabs, Nebius, Hyperbolic, Salad, RunPod.
// (Modal lives in modal_runner_adapter.go — it registers itself via its own init.)

// --- 13. Beam ---
// Auth: Beam token (Bearer). Free tier: free monthly hour allowance on T4/A10G for running containers.
// API: beam.ai REST v1 (beta) - /containers, /deployments. NOTE: endpoints inferred from public SDK.
func NewBeam() *apiAdapter {
	return &apiAdapter{
		id: "beam", name: "Beam", baseURL: "https://api.beam.ai/v1",
		caps: Capabilities{GPUs: []GPU{gpu("T4", 16, 1), gpu("L4", 24, 1)}, MaxDurationHours: 2,
			Frameworks: []string{"pytorch"}, Notebooks: false, Docker: true, SSH: false,
			FreeTierNotes: "Free monthly compute allowance (small GPU instances); sign-in with GitHub"},
		auth:     bearer,
		validate: "/whoami",
		submit:   "/containers",
		status:   func(id string) string { return "/containers/" + id },
		cancel:   func(id string) string { return "/containers/" + id },
	}
}

// --- 14. Intel Tiber AI Cloud ---
// Auth: API key (Bearer) from console.cloud.intel.com. Free tier: Gaudi2/Xe free quota program for developers.
// NOTE: Public REST surface limited; docs under Intel Tiber Developer. Endpoints best-effort.
func NewIntelTiber() *apiAdapter {
	return &apiAdapter{
		id: "intel-tiber", name: "Intel Tiber AI Cloud", baseURL: "https://cloud.intel.com/api/v1",
		caps: Capabilities{GPUs: []GPU{gpu("Gaudi2", 96, 1)}, MaxDurationHours: 12,
			Frameworks: []string{"pytorch", "tensorflow", "oneapi"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Free developer program: Gaudi2 accelerators (96GB) via Intel Tiber AI Cloud"},
		auth:     bearer,
		validate: "/projects", submit: "", // NOTE: no public job-submit REST; OpenShift-based notebooks
	}
}

// --- 15. Genesis Cloud ---
// NOTE: genesiscloud.com free trial - one-time credit, NOT permanent free tier. Included per spec list with NOTE.
// Auth: API key (Bearer). Real free tier does not exist as of 2026-11 docs check - adapter kept for completeness, gated behind enabled=false.
func NewGenesis() *apiAdapter {
	return &apiAdapter{
		id: "genesis", name: "Genesis Cloud", baseURL: "https://api.genesiscloud.com/compute/v1",
		caps: Capabilities{GPUs: []GPU{gpu("A100", 80, 1), gpu("H100", 80, 1)}, MaxDurationHours: 24,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "NO permanent free tier (one-time trial credit only) - verify before use"},
		auth:     bearer,
		validate: "/volumes", submit: "/instances",
		status: func(id string) string { return "/instances/" + id },
		cancel: func(id string) string { return "/instances/" + id },
	}
}

// --- 16. Anyscale ---
// NOTE: Anyscale (Ray) free tier ended in 2025 public docs; adapter kept with disabled default.
// Auth: API key + cloud name {"key":"...","cloud":"..."}.
func NewAnyscale() *apiAdapter {
	return &apiAdapter{
		id: "anyscale", name: "Anyscale", baseURL: "https://console.anyscale.com/api/v1",
		caps: Capabilities{GPUs: []GPU{gpu("A100", 40, 1)}, MaxDurationHours: 8,
			Frameworks: []string{"pytorch", "ray"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Free trial credits only (not a permanent free tier) - verify before use"},
		auth:     bearer,
		validate: "/clouds", submit: "/workspaces",
		status: func(id string) string { return "/workspaces/" + id },
		cancel: func(id string) string { return "/workspaces/" + id },
	}
}

// --- 17. JarvisLabs ---
// Auth: Bearer token from labs.jarvislabs.ai. Free tier: free small GPU credits for notebooks (limited instance-hours).
// API: /api/v1 instances (community-documented). NOTE: unofficial.
func NewJarvisLabs() *apiAdapter {
	return &apiAdapter{
		id: "jarvislabs", name: "JarvisLabs", baseURL: "https://labs.jarvislabs.ai/api/v1",
		caps: Capabilities{GPUs: []GPU{gpu("RTX4090", 24, 1), gpu("A100", 40, 1)}, MaxDurationHours: 8,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Free notebook credits (entry GPU) for new accounts; INR billing"},
		auth:     bearer,
		validate: "/user", submit: "/instances",
		status: func(id string) string { return "/instances/" + id },
		cancel: func(id string) string { return "/instances/" + id },
	}
}

// --- 18. Nebius Cloud ---
// NOTE: Nebius (ex-Yandex Cloud international) free tier: small always-free VM, no GPU. GPU free quota: no.
// Included per spec; GPU access is paid. Adapter kept disabled with NOTE. Auth: IAM token (Bearer).
func NewNebius() *apiAdapter {
	return &apiAdapter{
		id: "nebius", name: "Nebius Cloud", baseURL: "https://api.nebius.cloud/compute/v1",
		caps: Capabilities{GPUs: []GPU{gpu("H100", 80, 1), gpu("A100", 80, 1)}, MaxDurationHours: 24,
			Frameworks: []string{"pytorch"}, Notebooks: false, Docker: true, SSH: true,
			FreeTierNotes: "No free GPU quota in public tier - adapter present for completeness; disabled by default"},
		auth:     bearer,
		validate: "/instances", submit: "/instances",
		status: func(id string) string { return "/instances/" + id },
		cancel: func(id string) string { return "/instances/" + id },
	}
}

// --- 19. Hyperbolic ---
// NOTE: hyperbolic.xyz free tier: GPU marketplace per-second, free trial credits (one-time). Not permanent.
// Auth: Bearer key. API: /marketplace (OpenAI-compatible for inference - excluded category) but compute via rental API.
func NewHyperbolic() *apiAdapter {
	return &apiAdapter{
		id: "hyperbolic", name: "Hyperbolic", baseURL: "https://api.hyperbolic.xyz/v1",
		caps: Capabilities{GPUs: []GPU{gpu("A100", 80, 1), gpu("H100", 80, 1)}, MaxDurationHours: 12,
			Frameworks: []string{"pytorch"}, Notebooks: false, Docker: true, SSH: true,
			FreeTierNotes: "One-time trial credits, not a permanent free tier; verify before use"},
		auth:     bearer,
		validate: "/gpu-info", submit: "/instances",
		status: func(id string) string { return "/instances/" + id },
		cancel: func(id string) string { return "/instances/" + id },
	}
}

// --- 20. Salad Cloud ---
// Auth: API key (Bearer). Free tier: free community credits / sponsored GPU for inference ONLY - no notebook/compute.
// NOTE: Inference-focused; compute containers possible on free community tier at mercy of capacity. Included disabled.
func NewSalad() *apiAdapter {
	return &apiAdapter{
		id: "salad", name: "Salad Cloud", baseURL: "https://api.salad.com/apiserver/v1",
		caps: Capabilities{GPUs: []GPU{gpu("RTX4090", 24, 1), gpu("T4", 16, 1)}, MaxDurationHours: 4,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: false, Docker: true, SSH: false,
			FreeTierNotes: "Community-capacity free tier; unreliable availability; container jobs only"},
		auth:     bearer,
		validate: "/organizations", submit: "/container-groups",
		status: func(id string) string { return "/container-groups/" + id },
		cancel: func(id string) string { return "/container-groups/" + id },
	}
}

// --- 21. RunPod ---
// NOTE: RunPod has NO permanent free tier (one-time $1 credit). Compute-only provider. Adapter included per spec with NOTE; disabled by default.
func NewRunPod() *apiAdapter {
	return &apiAdapter{
		id: "runpod", name: "RunPod", baseURL: "https://api.runpod.io/graphql",
		caps: Capabilities{GPUs: []GPU{gpu("T4", 16, 1), gpu("RTX4090", 24, 1), gpu("A100", 80, 1)}, MaxDurationHours: 24,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "NO permanent free tier (one-time $1 credit) - not eligible; disabled by default"},
		auth:     bearer,
		validate: "", // GraphQL endpoint; validation via POST { query { myself { id } } } - not implemented, see NOTE
		submit:   "",
	}
}

func init() {
	registerBuiltin(func() []Provider {
		return []Provider{NewBeam(), NewIntelTiber(), NewGenesis(), NewAnyscale(),
			NewJarvisLabs(), NewNebius(), NewHyperbolic(), NewSalad(), NewRunPod()}
	})
}
