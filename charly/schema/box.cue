// CUE schema for the `box` kind. #Box validates ONE box entity (the value under
// `box:` in a discovered box/<distro>/box/<name>/charly.yml). Per-entity model.
// CLOSED (an unknown key is a typo). Shared defs (#Step/#Security/#Shell/#CalVer/
// #EntityRef/#EnvVar) come from _common.cue. Source of truth: charly/config.go
// BoxConfig (the `defaults:` block reuses BoxConfig but is NOT validated against
// #Box — only box ENTITIES are — so every BoxConfig field is modeled here even
// when only `defaults:` authors it).

// Package formats (BoxConfig.Build / BuildFormats). Named #BuildFormat to avoid
// colliding with distro.cue's #Format (the package-format definition struct).
#BuildFormat: "rpm" | "deb" | "pac" | "aur"

// Builder build-type slots (BoxConfig.Produce + BoxConfig.Builder keys).
#BuildType: "pixi" | "npm" | "cargo" | "aur"

// MergeConfig (config.go). CLOSED.
#BoxMerge: {
	auto?:         bool
	max_mb?:       int & >=0
	max_total_mb?: int & >=0
}

// AliasConfig (config.go). CLOSED. command defaults to name when omitted.
#BoxAlias: {
	name:     string & !=""
	command?: string & !=""
}

// base and from are mutually exclusive; neither is also valid (scratch box).
// matchN is applied via `&` (NOT embedded) so the struct literal stays CLOSED —
// an embedded matchN silently disables closedness.
#Box: {
	name:         #EntityRef
	version?:     #CalVer
	description?: string & !=""
	enabled?:     bool

	base?:                    string & !=""
	from?:                    string & =~"^builder:[a-z0-9]+(-[a-z0-9]+)*$"
	bootstrap_builder_image?: string & !=""

	platform?: [...(string & =~"^[a-z][a-z0-9]*/[a-z0-9]+")]
	tag?:      string & !=""
	registry?: string & !=""

	distro?: [...(string & !="")]
	build?:  #BuildFormat | [...#BuildFormat]

	candy?: [...#CandyRef]

	// box-level port: is RETIRED (rejectLegacyBoxPort) — ports inherit from candies.
	port?: _|_

	user?:        string & !=""
	uid?:         int & >=0
	gid?:         int & >=0
	user_policy?: *"auto" | "adopt" | "create"

	builder?: {[string]: string & !=""}
	produce?: [...#BuildType]

	env?: [...#EnvVar]
	env_file?:   string & !=""
	security?:   #Security
	network?:    string & !=""
	init?:       "supervisord" | "systemd"
	data_image?: bool

	jobs?:            int & >=0
	podman_jobs?:     int & >=0
	podman_jobs_cap?: int & >=0
	context_ignore?: [...(string & !="")]
	cache?:           "image" | "registry" | "gha" | "none"
	keep_images?:     int & >=0
	keep_check_runs?: int & >=0

	merge?: #BoxMerge
	alias?: [...#BoxAlias]

	plan?: [...#Step]
	check_level?: *"noagent" | "none" | "build" | "agent"

	shell?: #Shell
} & ({from?: _|_} | {base?: _|_}) // at most one of base/from (disjunction keeps it CLOSED; matchN would open it)
