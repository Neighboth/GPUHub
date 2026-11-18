package providers

import "sort"

var builtins []func() []Provider

func registerBuiltin(f func() []Provider) { builtins = append(builtins, f) }

// baseURLOverrides lets tests point adapters at mock servers. Not used in production.
var baseURLOverrides = map[string]string{}

// OverrideBaseURL redirects all subsequent constructions of the given adapter at the mock URL.
// Used by tests only (httptest servers).
func OverrideBaseURL(id, url string) { baseURLOverrides[id] = url }

// All returns every registered built-in adapter (default settings).
func All() []Provider {
	var out []Provider
	for _, f := range builtins {
		out = append(out, f()...)
	}
	for _, p := range out {
		if a, ok := p.(*apiAdapter); ok {
			if u, ok := baseURLOverrides[a.id]; ok && u != "" {
				a.baseURL = u
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Lookup returns the adapter with the given id, or nil.
func Lookup(id string) Provider {
	for _, p := range All() {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// DefaultDisabled lists provider ids that are included as adapters but have no permanent
// free tier (or a discontinued service) and are therefore seeded into the DB as disabled.
var DefaultDisabled = map[string]bool{
	"genesis": true, "anyscale": true, "nebius": true, "hyperbolic": true,
	"salad": true, "runpod": true, "codesphere": true, "replit": true,
	"deepnote": true, "hex": true, "noteable": true, "domino": true,
	"floydhub": true, "spell": true, "valohai": true,
	"sagemaker-studio-lab": true, // closed to new customers since 2026-07
}
