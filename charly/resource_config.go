package main

// ResourceDef declares a named exclusive host-resource — the token used by a
// deploy/bed's `requires_exclusive:` (claimant) and a holder's
// `preemptible.holds:` — PLUS an optional hardware selector that lets
// `charly vm create` AUTO-ALLOCATE the matching physical device when a
// VM-targeted claimant needs it. The token name is the operator's choice; the
// selector is what turns "I need the nvidia-gpu token" into a concrete PCI
// `<hostdev>`.
//
// YAML-configured in the embedded build vocabulary (charly/charly.yml) — the
// selector lives in config, never hardcoded in Go:
//
//	resource:
//	  nvidia-gpu:            # SAME token used in requires_exclusive / preemptible.holds
//	    gpu:
//	      vendor: "0x10de"   # PCI vendor DetectVFIO matches (NVIDIA)
//
// A resource with no selector is still valid — it names a free/abstract token
// the arbiter contends over (preempt.go) with no auto-allocation. A resource
// WITH a `gpu:` selector drives auto-allocation + fail-hard at vm create.
type ResourceDef struct {
	Gpu *GpuSelector `yaml:"gpu,omitempty" json:"gpu,omitempty"`
}

// GpuSelector matches a passthrough-capable GPU by PCI vendor (e.g. "0x10de"
// = NVIDIA). DetectVFIO reports each GPU's VendorID in the same 0x-prefixed
// lowercase hex form. Vendor is REQUIRED on a gpu selector (validated).
type GpuSelector struct {
	Vendor string `yaml:"vendor" json:"vendor"`
}

// HasSelector reports whether this resource carries a hardware selector that
// drives auto-allocation (vs a bare arbitration token).
func (r *ResourceDef) HasSelector() bool {
	return r != nil && r.Gpu != nil
}

// ResourceDoc wraps a single ResourceDef with an explicit Name — the
// `kind: resource` + `name: <token>` standalone form. Mirrors DistroDoc.
type ResourceDoc struct {
	Name        string `yaml:"name" json:"name"`
	ResourceDef `yaml:",inline"`
}
