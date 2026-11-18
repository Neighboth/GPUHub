package providers

// Part 3: Codesphere, Replit, Deepnote, Hex, Noteable, Domino, FloydHub, Spell, Valohai, Determined, ClearML + extra free-tier GPU sites.

// --- 22. Codesphere ---
// Auth: Bearer token from codesphere.com. Free tier: free cloud dev environments with 1 free small GPU credits? NOTE: CPU-only free; GPU is paid. Included disabled with NOTE.
func NewCodesphere() *apiAdapter {
	return &apiAdapter{
		id: "codesphere", name: "Codesphere", baseURL: "https://api.codesphere.com/api/v1",
		caps: Capabilities{GPUs: capT4, MaxDurationHours: 4,
			Frameworks: []string{"pytorch"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Free tier is CPU-only; GPU workspaces are paid — verify before use"},
		auth:     bearer,
		validate: "/me", submit: "/workspaces",
		status: func(id string) string { return "/workspaces/" + id },
		cancel: func(id string) string { return "/workspaces/" + id },
	}
}

// --- 23. Replit ---
// NOTE: Replit free tier: CPU only (no GPU). Hacker plan required for cycles. Included per spec with NOTE; disabled.
func NewReplit() *apiAdapter {
	return &apiAdapter{
		id: "replit", name: "Replit", baseURL: "https://replit.com/api/v1",
		caps: Capabilities{GPUs: capT4, MaxDurationHours: 2,
			Frameworks: []string{"pytorch"}, Notebooks: false, Docker: false, SSH: false,
			FreeTierNotes: "Free tier has NO GPU — disabled by default; GPU requires paid Hacker plan"},
		auth:     bearer,
		validate: "", submit: "",
	}
}

// --- 24. Deepnote ---
// Auth: API key (Bearer) from deepnote.com. Free tier: shared vCPU notebook only — no free GPU. Included disabled with NOTE.
func NewDeepnote() *apiAdapter {
	return &apiAdapter{
		id: "deepnote", name: "Deepnote", baseURL: "https://api.deepnote.com/v1",
		caps: Capabilities{GPUs: capT4, MaxDurationHours: 2,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "Free tier is CPU-only — GPU machines are paid; disabled by default"},
		auth:     bearer,
		validate: "/me", submit: "/projects",
		status: func(id string) string { return "/projects/" + id },
		cancel: func(id string) string { return "/projects/" + id },
	}
}

// --- 25. Hex ---
// NOTE: hex.tech — free tier for collaborative notebooks/SQL; no free GPU (paid compute only). Included disabled with NOTE.
func NewHex() *apiAdapter {
	return &apiAdapter{
		id: "hex", name: "Hex", baseURL: "https://app.hex.tech/api/v1",
		caps: Capabilities{GPUs: capT4, MaxDurationHours: 2,
			Frameworks: []string{"pytorch"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "Free tier has no GPU — paid compute plans only; disabled by default"},
		auth:     bearer,
		validate: "/me", submit: "",
	}
}

// --- 26. Noteable ---
// NOTE: noteable.io — free tier: CPU notebooks only, no free GPU. Included disabled with NOTE.
func NewNoteable() *apiAdapter {
	return &apiAdapter{
		id: "noteable", name: "Noteable", baseURL: "https://api.noteable.io/v1",
		caps: Capabilities{GPUs: capT4, MaxDurationHours: 2,
			Frameworks: []string{"pytorch"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "Free tier has no GPU — disabled by default"},
		auth:     bearer,
		validate: "/me", submit: "",
	}
}

// --- 27. Domino Datalab ---
// NOTE: dominodatalab.com — free trial (one-time credits), not permanent; on-prem free for open source projects. Included disabled.
func NewDomino() *apiAdapter {
	return &apiAdapter{
		id: "domino", name: "Domino Datalab", baseURL: "https://app.dominodatalab.com/v1",
		caps: Capabilities{GPUs: []GPU{gpu("A100", 40, 1), gpu("V100", 16, 1)}, MaxDurationHours: 8,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Trial credits only; free for qualifying OSS projects — verify eligibility"},
		auth:     bearer,
		validate: "/users/current", submit: "/jobs",
		status: func(id string) string { return "/jobs/" + id },
		cancel: func(id string) string { return "/jobs/" + id + "/stop" },
	}
}

// --- 28. FloydHub ---
// NOTE: FloydHub shut down (product discontinued). Adapter preserved for reference only; never registered enabled.
func NewFloydHub() *apiAdapter {
	return &apiAdapter{
		id: "floydhub", name: "FloydHub (discontinued)", baseURL: "https://www.floydhub.com/api/v1",
		caps: Capabilities{GPUs: []GPU{gpu("V100", 16, 1)}, MaxDurationHours: 6,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: false,
			FreeTierNotes: "SERVICE DISCONTINUED — kept as skeleton only; do not use"},
		auth:     bearer,
		validate: "", submit: "",
	}
}

// --- 29. Spell.ml ---
// NOTE: spell.ml became Determined AI / HPE MLDE after acquisition. Redirected adapter — use Determined instead.
func NewSpell() *apiAdapter {
	return &apiAdapter{
		id: "spell", name: "Spell.ml (now Determined)", baseURL: "https://api.spell.ml/v1",
		caps: Capabilities{GPUs: capT4, MaxDurationHours: 4,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Service rebranded; free tier unclear — see Determined AI adapter"},
		auth:     bearer,
		validate: "", submit: "",
	}
}

// --- 30. Valohai ---
// NOTE: valohai.com — free tier: small project with 10 free compute-hours (GPU not guaranteed). Trial-ish. Included disabled.
func NewValohai() *apiAdapter {
	return &apiAdapter{
		id: "valohai", name: "Valohai", baseURL: "https://app.valohai.com/api/v1",
		caps: Capabilities{GPUs: capAnyFreeSmall, MaxDurationHours: 2,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: false, Docker: true, SSH: false,
			FreeTierNotes: "Free tier: 10 compute-hours trial project; GPU not guaranteed"},
		auth:     bearer,
		validate: "/projects", submit: "/executions",
		status: func(id string) string { return "/executions/" + id },
		cancel: func(id string) string { return "/executions/" + id + "/stop" },
	}
}

// --- 31. Determined AI (open source self-hosted) ---
// Determined is open source; self-host free on own GPUs. Adapter targets a Determined master endpoint.
func NewDetermined() *apiAdapter {
	return &apiAdapter{
		id: "determined", name: "Determined AI (self-hosted)", baseURL: "http://localhost:8080/api/v1",
		caps: Capabilities{GPUs: capSelfHost, MaxDurationHours: 0,
			Frameworks: []string{"pytorch", "tensorflow"}, Notebooks: true, Docker: true, SSH: true,
			FreeTierNotes: "Open source (Apache-2.0) — free on your own GPUs; point base_url at your master"},
		auth:     bearer,
		validate: "/users/me", submit: "/experiments",
		status: func(id string) string { return "/experiments/" + id },
		cancel: func(id string) string { return "/experiments/" + id + "/archive" },
	}
}

// --- 32. ClearML ---
// Open source MLOps (clearml.io); free hosted tier (app.clear.ml) includes agent minutes on your own machines.
// NOTE: ClearML free hosted = orchestration, not GPU supply. GPUs come from your agents. Included with NOTE.
func NewClearML() *apiAdapter {
	return &apiAdapter{
		id: "clearml", name: "ClearML", baseURL: "https://api.app.clear.ml/api/v2.25",
		caps: Capabilities{GPUs: capSelfHost, MaxDurationHours: 0,
			Frameworks: []string{"pytorch", "tensorflow", "jax"}, Notebooks: true, Docker: true, SSH: false,
			FreeTierNotes: "Free hosted orchestration; GPU comes from your own ClearML agents (no hosted GPU)"},
		auth: func(p CredentialPayload) (string, string) {
			// ClearML uses access/secret as HTTP basic for API calls.
			return basicAuth(p)
		},
		validate: "/users/GetCurrentUser", submit: "/tasks/id",
		status: func(id string) string { return "/tasks/" + id },
		cancel: func(id string) string { return "/tasks.stop" },
	}
}

// --- Extra free-tier GPU compute providers discovered during research (spec §3) ---

// --- 33. Google Colab (Enterprise is paid) — placeholder covered by NewColab; see README ---

// --- Kaggle TPU / free notebooks already covered ---

// --- 34. Paperspace Free Gradient (already above) ---

// --- 35. Lightning AI — covered ---

func init() {
	registerBuiltin(func() []Provider {
		return []Provider{NewCodesphere(), NewReplit(), NewDeepnote(), NewHex(), NewNoteable(), NewDomino(),
			NewFloydHub(), NewSpell(), NewValohai(), NewDetermined(), NewClearML()}
	})
}
