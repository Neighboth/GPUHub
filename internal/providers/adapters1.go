package providers

// Adapter constructors. Only providers with a genuine free tier offering compute/notebook GPU
// are registered. See README.md for per-provider verification notes (checked against public docs, 2026-11).
//
// NOTE for readers: many free-tier GPU providers (Kaggle, Colab, Studio Lab) have NO official
// job-submission API. For those, the adapter performs best-effort calls to the documented/
// commonly-used endpoints; where none exist it returns a descriptive error instead of faking success.
// GPUHub still tracks quota, scheduling and failover for API-capable providers.

func gpu(t string, vram, max int) GPU { return GPU{Type: t, VRAMGB: vram, Count: max, MaxCount: max} }

var (
	capT4           = []GPU{gpu("T4", 16, 1)}
	capT4P100       = []GPU{gpu("P100", 16, 1), gpu("T4", 16, 1)}
	capT4L4         = []GPU{gpu("T4", 16, 1), gpu("L4", 24, 1)}
	capA100         = []GPU{gpu("A100", 40, 1)}
	capH100         = []GPU{gpu("H100", 80, 1)}
	capRTX4090      = []GPU{gpu("RTX4090", 24, 1)}
	capT4A100       = []GPU{gpu("T4", 16, 1), gpu("A100", 40, 1)}
	capV100         = []GPU{gpu("V100", 16, 1)}
	capL4           = []GPU{gpu("L4", 24, 1)}
	capAnyFreeSmall = []GPU{gpu("T4", 16, 8), gpu("P100", 16, 8), gpu("V100", 16, 8), gpu("L4", 24, 8)}
	capSelfHost     = []GPU{gpu("T4", 16, 8), gpu("P100", 16, 8), gpu("V100", 16, 8), gpu("L4", 24, 8), gpu("A100", 80, 8), gpu("H100", 80, 8), gpu("RTX4090", 24, 8), gpu("RTX6000Pro", 96, 8)}
)

// --- 1. Kaggle Notebooks ---
// Auth: JSON {"username":"...","key":"..."} (kaggle.json). Free tier: 2x T4 or P100, ~30 GPU h/week quota.
// NOTE: No official public API for launching notebook sessions; adapter uses the documented
// REST endpoints of the Kaggle API for identity validation and reports unsupported submits.
func NewKaggle() *apiAdapter {
	return &apiAdapter{
		id: "kaggle", name: "Kaggle Notebooks", baseURL: "https://www.kaggle.com/api/v1",
		caps: Capabilities{GPUs: []GPU{gpu("T4", 16, 2)}, MaxDurationHours: 12,
			Frameworks: []string{"pytorch", "tensorflow", "jax"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "30 GPU-hours/week (floating; T4 x2 16GB — only GPU, P100 retired 2026-09); phone-verified; 12h max session; /kaggle/working persists 20GB"},
		auth:     kaggleAuth,
		validate: "/competitions/list?page=1",
		quota:    "", // not exposed — tracked locally by GPUHub (key_usage)
		submit:   "", // NOTE: Kaggle has no public job-submission API; submit via UI. Sessions are notebook-only.
	}
}

// --- 2. Google Colab ---
// NOTE: No official API; the community uses the unofficial Jupyter protocol against a Colab runtime URL
// obtained by the user from colab.google.com. Free tier: T4 (12h sessions) / K80 legacy.
func NewColab() *apiAdapter {
	return &apiAdapter{
		id: "colab", name: "Google Colab", baseURL: "",
		caps: Capabilities{GPUs: capT4, MaxDurationHours: 12,
			Frameworks: []string{"pytorch", "tensorflow", "jax"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "Free T4 GPU, ~12h session, dynamic limits; usage not guaranteed"},
		auth:     tokenHeader("Authorization"), // user pastes runtime token from colab.google.com (unofficial)
		validate: "", submit: "",
	}
}

// --- 3. Lightning AI ---
// Auth: API key (Bearer) from lightning.ai. Free tier: monthly GPU credits on T4/A10G dev machines.
func NewLightning() *apiAdapter {
	return &apiAdapter{
		id: "lightning", name: "Lightning AI", baseURL: "https://lightning.ai/api/v1",
		caps: Capabilities{GPUs: []GPU{gpu("T4", 16, 1), gpu("L4", 24, 1), gpu("L40S", 48, 1), gpu("A100", 40, 1), gpu("H200", 141, 1)}, MaxDurationHours: 4,
			Frameworks: []string{"pytorch", "tensorflow", "jax"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Free credits every month (~75 T4 GPU-hours, 15 credits); T4→A100/H200 quota by account; 1 free Studio, restarts ~4h; card needed for more credits"},
		auth:     bearer,
		validate: "/users/me",
		submit:   "/studios", // NOTE: unofficial; actual run orchestration is Studio-based (SSH into dev machine)
	}
}

// --- 4. Paperspace Gradient ---
// Auth: API key {"key":"..."} (x-api-key header). Free tier: free GPU on Gradient Notebooks (M4000/P4000 class / T4 on some plans).
func NewPaperspace() *apiAdapter {
	return &apiAdapter{
		id: "paperspace", name: "Paperspace Gradient", baseURL: "https://api.paperspace.com",
		caps: Capabilities{GPUs: []GPU{gpu("M4000", 8, 1)}, MaxDurationHours: 6,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: false,
			FreeTierNotes: "Free plan: 1 free GPU (M4000, 8GB — Maxwell, slow) + free CPU (C4); 6h auto-shutdown; private workspaces, notebooks only; Pro $8/mo adds P4000/RTX4000/A4000 free GPU types"},
		auth:     tokenHeader("x-api-key"),
		validate: "/users/me",
		submit:   "/jobs",
		status:   func(id string) string { return "/jobs/" + id },
		logs:     func(id string) string { return "/jobs/" + id + "/logs" },
		cancel:   func(id string) string { return "/jobs/" + id + "/stop" },
	}
}

// --- 5. Saturn Cloud ---
// Auth: token from app.saturncloud.io (Authorization: Bearer). Free tier: 30 GPU-hours/month on T4.
func NewSaturn() *apiAdapter {
	return &apiAdapter{
		id: "saturn", name: "Saturn Cloud", baseURL: "https://api.saturncloud.io/api/v1",
		caps: Capabilities{GPUs: []GPU{gpu("T4", 16, 1)}, MaxDurationHours: 4,
			Frameworks: []string{"pytorch", "tensorflow", "dask"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Hosted Free: 150 shared GPU+CPU hours/month (T4 16GB only); JupyterLab/RStudio/SSH/Dask; no card; idle auto-shutdown"},
		auth:     bearer,
		validate: "/users/me/",
		submit:   "/jupyter-servers/",
		status:   func(id string) string { return "/jupyter-servers/" + id + "/" },
		cancel:   func(id string) string { return "/jupyter-servers/" + id + "/stop/" },
	}
}

// --- 6. Amazon SageMaker Studio Lab ---
// NOTE: Studio Lab has no public job API. Free tier: 4 GPU-hours/session, T4 or G4dn. Sessions via UI only.
// Auth: none usable via API; credential is treated as a session cookie/token for manual bookkeeping only.
func NewSageMakerStudioLab() *apiAdapter {
	return &apiAdapter{
		id: "sagemaker-studio-lab", name: "SageMaker Studio Lab", baseURL: "https://studiolab.sagemaker.aws.amazon.com",
		caps: Capabilities{GPUs: capT4, MaxDurationHours: 4,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "CLOSED to new customers since 2026-07 (AWS migrated Studio Lab to SageMaker Studio free tier, 250 CPU-h/mo); existing accounts still work 4h GPU sessions (T4)"},
		auth:     tokenHeader("Cookie"),
		validate: "", submit: "",
	}
}

// --- 7. Paddle AI Studio (Baidu) ---
// NOTE: Chinese portal (aistudio.baidu.com). Free compute: Tesla V100 / 40GB for learners (limited hours).
// Auth: access token from AI Studio console. API surface unofficial/reverse for jobs; validation endpoint documented.
func NewPaddleAIStudio() *apiAdapter {
	return &apiAdapter{
		id: "paddle-aistudio", name: "Paddle AI Studio", baseURL: "https://aistudio.baidu.com/api",
		caps: Capabilities{GPUs: []GPU{gpu("V100", 16, 1)}, MaxDurationHours: 12,
			Frameworks: []string{"paddlepaddle", "pytorch"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "Free V100 16GB activations; daily 12h session cap; China account (phone); PaddlePaddle-first ecosystem"},
		auth:     tokenHeader("Authorization"),
		validate: "/v1/user/info", submit: "", // NOTE: no public job API; submission via web UI
	}
}

// --- 8. ModelScope (Alibaba) ---
// NOTE: modelscope.cn provides free GPU notebook instances (16-24h, T4/A10 class).
// Auth: SDK token (ModelScope SDK uses 'Authorization: Bearer <token>'). Jobs launched via SDK, not REST.
func NewModelScope() *apiAdapter {
	return &apiAdapter{
		id: "modelscope", name: "ModelScope", baseURL: "https://modelscope.cn/api/v1",
		caps: Capabilities{GPUs: []GPU{gpu("A10", 24, 1), gpu("MI300X", 192, 1)}, MaxDurationHours: 10,
			Frameworks: []string{"pytorch"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "36h one-time NVIDIA A10 24GB quota (new users, PAI-DSW, 10h per run, 1h idle timeout); +100h AMD MI300X 192GB via AMD Developer program (ROCm)"},
		auth:     bearer,
		validate: "/users/current", submit: "", // NOTE: submit via ModelScope SDK (ms). Not a REST job API.
	}
}

// --- 9. Tencent CloudStudio ---
// NOTE: cloudstudio.tencent.com free cloud dev environments incl. some GPU SKUs (availability varies by region).
// Auth: token from CloudStudio console.
func NewCloudStudio() *apiAdapter {
	return &apiAdapter{
		id: "cloudstudio", name: "Tencent CloudStudio", baseURL: "https://cloudstudio.tencent.com/api",
		caps: Capabilities{GPUs: []GPU{gpu("T4", 16, 1)}, MaxDurationHours: 8,
			Frameworks: []string{"pytorch"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "~10000 free GPU-minutes/month (~166h, T4 16GB), region-dependent availability; Tencent account + real-name verification; 8h sessions"},
		auth:     tokenHeader("Authorization"),
		validate: "", submit: "",
	}
}

// --- 10. Leafcloud ---
// NOTE: leaf.cloud — sustainable public cloud; free research/compute credits program for GPU workloads.
// Auth: API key (Bearer) from Leafcloud dashboard.
func NewLeafcloud() *apiAdapter {
	return &apiAdapter{
		id: "leafcloud", name: "Leafcloud", baseURL: "https://api.leafcloud.com/v1",
		caps: Capabilities{GPUs: capT4L4, MaxDurationHours: 6,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: false, Docker: true, SSH: true,
			FreeTierNotes: "Free research-credit GPU program (T4/L4); application required"},
		auth:     bearer,
		validate: "/whoami",
		submit:   "/instances",
		status:   func(id string) string { return "/instances/" + id },
		cancel:   func(id string) string { return "/instances/" + id },
	}
}

// --- 11. Hugging Face Spaces (ZeroGPU) ---
// Auth: HF token (Bearer). ZeroGPU: free H100-time on shared Spaces (queue-based) for public Spaces.
// API: HfApi for Spaces; ZeroGPU attach is code-level (@spaces.GPU), so GPUHub tracks usage locally.
func NewHFSpaces() *apiAdapter {
	return &apiAdapter{
		id: "hf-spaces", name: "Hugging Face Spaces (ZeroGPU)", baseURL: "https://huggingface.co/api",
		caps: Capabilities{GPUs: []GPU{gpu("RTX6000Pro", 96, 1)}, MaxDurationHours: 1,
			Frameworks: []string{"pytorch", "tensorflow", "jax", "gradio"}, Notebooks: false, Docker: true, SSH: false,
			FreeTierNotes: "ZeroGPU: shared NVIDIA RTX PRO 6000 Blackwell — large=48GB (1×quota), xlarge=96GB (2×quota). Free accounts: 5 GPU-min/day (~3 runs); PRO 40min/day; resets 24h after first GPU use; Gradio SDK only; 60s/call default"},
		auth:     bearer,
		validate: "/whoami-v2",
		submit:   "", // NOTE: ZeroGPU attaches inside the Space app; no external job-submit API
	}
}

func init() {
	registerBuiltin(func() []Provider {
		return []Provider{NewKaggle(), NewColab(), NewLightning(), NewPaperspace(), NewSaturn(),
			NewSageMakerStudioLab(), NewPaddleAIStudio(), NewModelScope(), NewCloudStudio(), NewLeafcloud(), NewHFSpaces()}
	})
}
