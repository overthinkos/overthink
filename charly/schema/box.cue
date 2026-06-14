// CUE schema for the `box` kind. #Box validates ONE box entity (the value under
// `box:` in a discovered box/<distro>/box/<name>/charly.yml). Per-entity model.
// OPEN tail; real fields constrained. Shared #Step from _common.cue. Source of
// truth: charly/config.go BoxConfig.

// Package formats (BoxConfig.Build / BuildFormats). Named #BuildFormat to avoid
// colliding with distro.cue's #Format (the package-format definition struct).
#BuildFormat: "rpm" | "deb" | "pac" | "aur"

// Builder build-type slots (BoxConfig.Produce + BoxConfig.Builder keys).
#BuildType: "pixi" | "npm" | "cargo" | "aur"

#BoxSecurity: {
	privileged?: bool
	cgroupns?:   string & !=""
	cap_add?: [...(string & !="")]
	devices?: [...(string & !="")]
	security_opt?: [...(string & !="")]
	ipc_mode?: string & !=""
	shm_size?: string & !=""
	group_add?: [...(string & !="")]
	mount?: [...(string & !="")]
	memory_max?:      string & !=""
	memory_high?:     string & !=""
	memory_swap_max?: string & !=""
	cpus?:            string & !=""
	...
}

#BoxMerge: {
	auto?:         bool
	max_mb?:       int & >=0
	max_total_mb?: int & >=0
	...
}

#BoxAlias: {
	name:     string & !=""
	command?: string
	...
}

#BoxShellSpec: {
	init?: string
	path_append?: [...string]
	path?: string
	...
}

#BoxShell: {
	init?: string
	path_append?: [...string]
	path?:     string
	priority?: int
	bash?:     #BoxShellSpec
	zsh?:      #BoxShellSpec
	fish?:     #BoxShellSpec
	sh?:       #BoxShellSpec
	...
}

#Box: {
	name:         string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	version?:     string & =~"^[0-9]{4}\\.[0-9]{1,3}\\.[0-9]{3,4}$"
	description?: string & !=""
	enabled?:     bool

	base?:                    string & !=""
	from?:                    string & =~"^builder:[a-z0-9]+(-[a-z0-9]+)*$"
	bootstrap_builder_image?: string & !=""
	// base and from are mutually exclusive; neither is also valid (scratch box).
	matchN(<=1, [{base!: _}, {from!: _}])

	platform?: [...(string & =~"^[a-z][a-z0-9]*/[a-z0-9]+")]
	tag?:      string & !=""
	registry?: string & !=""

	distro?: [...(string & !="")]
	build?:  #BuildFormat | [...#BuildFormat]

	candy?: [...(string & !="")]

	// box-level port: is RETIRED (rejectLegacyBoxPort) — ports inherit from candies.
	port?: _|_

	user?:        string & !=""
	uid?:         int & >=0
	gid?:         int & >=0
	user_policy?: *"auto" | "adopt" | "create"

	builder?: {[string]: string & !=""}
	produce?: [...#BuildType]

	env?: [...(string & !="")]
	env_file?:   string & !=""
	security?:   #BoxSecurity
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

	shell?: #BoxShell
	...
}
