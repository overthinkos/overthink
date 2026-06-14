// CUE schema for the `local` kind (kind:local templates — target:local candy
// stacks). #Local validates ONE entry of the `local:` map. CLOSED (the Go
// loader decodes with KnownFields(true), so unknown keys are an error). Shared
// #Step from _common.cue.

#Local: {
	// Ordered candy stack applied to the host. Required (empty list permitted —
	// a staged name-reservation stub; the loader warns, not errors).
	candy!: [...#CandyRef]
	install_opts?: #InstallOpts
	env?: [...#EnvVar]
	description?: string & !=""
	plan?: [...#Step]
}

// A candy ref: bare lowercase-hyphenated name OR a remote @github…:vTAG ref.
#CandyRef: =~"^(@.+|[a-z0-9]+(-[a-z0-9]+)*)$"

// An env entry: KEY=VALUE (value may be empty / contain =).
#EnvVar: =~"^[A-Za-z_][A-Za-z0-9_]*=.*$"

// install_opts gates (deploy.go InstallOptsConfig). Singular with_service
// (charly migrate rewrote the legacy plural with_services).
#InstallOpts: {
	with_service?:       bool
	allow_repo_changes?: bool
	allow_root_tasks?:   bool
	skip_incompatible?:  bool
	verify?:             bool
	builder_image?:      string & !=""
}
