package main

// check_score_kind.go — the `iterate:` block (the AI-loop orchestration).
//
// The AI-loop orchestration — the agent list, plateau policy, prompt,
// sandbox, MCP endpoint, notes toggle, env — lives in an `IterateConfig`
// carried on a `deploy:` entry or a `kind: check` bed (DeploymentNode.Iterate).
// `charly check run <entity>`: when the entity carries an `iterate:` block the
// AI loop drives it (scoring the entity's own `plan:` check:/agent-check:
// steps); otherwise the entity is a plain `kind: check` bed and runs the
// deterministic R10 sequence.
//
// The scored content is the entity's OWN `plan:` (baked + include:'d + inline)
// — there is no separate catalog to resolve.

// IterateConfig is the AI-loop orchestration block.
type IterateConfig struct {
	// Eligible agent names (must reference entries in the `agent:` map).
	Agent []string `yaml:"agent,omitempty"`

	// Sandbox names the deployment (pod | vm | host) where the AGENT + charly
	// run — the former score `pod:`/`vm:`/`host:` discriminator collapsed to a
	// single deploy-ref name. Its target kind is resolved from the named
	// deployment entry.
	Sandbox string `yaml:"sandbox,omitempty"`

	// PlateauIteration is the only loop bound. The loop exits after this many
	// consecutive non-improving iterations. 0 disables plateau detection.
	PlateauIteration int `yaml:"plateau_iteration,omitempty"`

	// Prompt template. Standard ${TOKEN} substitution applied per iter.
	Prompt string `yaml:"prompt,omitempty"`

	// Note controls the persistent NOTES.md memory subsystem. Pointer so the
	// default (true) and an explicit `false` are distinguishable.
	Note *bool `yaml:"note,omitempty"`

	// Env is free-form substitution env — each KEY becomes a ${KEY} token.
	Env map[string]string `yaml:"env,omitempty"`

	// MCPEndpoint drives the canonical ${MCP_ENDPOINT} substitution. Pointer
	// so unset (use default) and set-to-"" (disable) differ on the wire.
	MCPEndpoint *string `yaml:"mcp_endpoint,omitempty"`

	// ValidateAiArtifacts narrows artifact-producing state-dependent probes to
	// validate the AI's iteration artifact instead of re-running the capture.
	// See the runner's runCharlyVerb artifact branch. Default false.
	ValidateAiArtifacts bool `yaml:"validate_ai_artifacts,omitempty"`
}

// NotesEnabled returns true unless the iterate block explicitly opts out.
func (i *IterateConfig) NotesEnabled() bool {
	if i == nil || i.Note == nil {
		return true
	}
	return *i.Note
}

// EffectiveMCPEndpoint resolves the canonical ${MCP_ENDPOINT} value.
//
//   - nil pointer (unset)        → DefaultMCPEndpoint
//   - non-nil, value=""          → "" (disabled by author)
//   - non-nil, value=<something> → that value verbatim
func (i *IterateConfig) EffectiveMCPEndpoint() string {
	if i == nil || i.MCPEndpoint == nil {
		return DefaultMCPEndpoint
	}
	return *i.MCPEndpoint
}

// DefaultMCPEndpoint is the canonical charly-mcp bind URL.
const DefaultMCPEndpoint = "http://localhost:18765/mcp"

// ---------------------------------------------------------------------------
// Target discriminator
// ---------------------------------------------------------------------------

// TargetKind identifies which executor backend the iterate sandbox uses.
type TargetKind string

const (
	TargetKindPod  TargetKind = "pod"
	TargetKindVM   TargetKind = "vm"
	TargetKindHost TargetKind = "host"
)

// ResolveIterateSandbox classifies the iterate sandbox's target kind by
// looking up the named deployment in the project's Deploy map. A bare host
// sandbox (target: local with host: local) resolves to TargetKindHost; an
// explicit vm: deploy → TargetKindVM; everything else (the default pod) →
// TargetKindPod. The returned name is the sandbox deploy name (empty for a
// host sandbox).
func ResolveIterateSandbox(uf *UnifiedFile, sandbox string) (TargetKind, string) {
	if sandbox == "" {
		return TargetKindHost, ""
	}
	if uf != nil {
		// foldCheckBeds copies kind:check beds into Deploy, so one lookup
		// covers both deploys and beds.
		if node, ok := uf.Deploy[sandbox]; ok {
			return targetKindForNode(&node), sandbox
		}
		if node, ok := uf.Check[sandbox]; ok {
			return targetKindForNode(&node), sandbox
		}
	}
	return TargetKindPod, sandbox
}

// targetKindForNode maps a DeploymentNode's target to a TargetKind.
func targetKindForNode(node *DeploymentNode) TargetKind {
	switch node.Target {
	case "vm":
		return TargetKindVM
	case "local", "host":
		return TargetKindHost
	default:
		return TargetKindPod
	}
}
