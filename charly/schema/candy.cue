// CUE schema for the `candy` kind. #Candy validates ONE candy entity (the value
// under `candy:` in a kind-keyed candy charly.yml). CLOSED (an unknown top-level
// key is a typo) — this replaces the Go UnmarshalYAML known-field typo guard
// (layers.go candyYAMLKnownFields). Every key in candyYAMLKnownFields is modeled.
// Shared defs (#Step/#Security/#Shell/#CalVer/#EntityRef/#CandyRef/#PackageItem/
// #DistroPackages) come from _common.cue. Source of truth: charly/layers.go
// CandyYAML + its sub-types (PortSpec, VolumeYAML, AliasYAML, ExtractYAML,
// DataYAML, RouteYAML, EnvDependency, MCPServerYAML, SecretYAML, HooksConfig,
// CandyArtifact, CandyCapabilities, ApkPackageSpec, LocalPkgMap, ServiceEntry).

#Candy: {
	// --- identity (required: ADE mandates version+name+description+plan) ---
	version:     #CalVer
	name?:       #EntityRef
	description: string & !=""
	plan?: [...#Step]

	// --- maturity / engine / legacy-tolerated ---
	status?: *"testing" | "working" | "broken"
	engine?: "docker" | "podman"
	from?:   string // tolerated known key (layers.go); decoded into nothing, no meaning
	reboot?: bool

	// --- composition ---
	candy?: [...#CandyRef]
	require?: [...#CandyRef]
	requires_capability?: [...(string & !="")]
	capability?: #CandyCapability

	// --- runtime env / local vars / PATH ---
	// env forbids PATH (validate.go: use path_append instead). Values are
	// Go-coerced scalars (#StrVal) — an unquoted `PORT: 8080` is a string.
	env?: {PATH?: _|_, [string]: #StrVal}
	var?: {[=~"^[A-Z_][A-Z0-9_]*$"]: #StrVal}
	path_append?: [...(string & !="")]

	// --- package surface (Calamares-aligned) ---
	package?: [...#PackageItem]
	distro?: {[string]: #DistroPackages}
	apk?: [...#CandyApk]
	// localpkg maps a native package FORMAT to a bundled source dir; a scalar
	// form is rejected by Go. Closed to the three known formats.
	localpkg?: {
		pac?: string & !=""
		rpm?: string & !=""
		deb?: string & !=""
	}

	// --- networking / routing ---
	// PortSpec: a plain int OR a "proto:port" string (proto ∈ http/https/tcp/…).
	port?: [...(int & >0 & <=65535 | string & =~"^[a-z+-]+:[0-9]+$")]
	port_relay?: [...(int & >0 & <=65535)]
	route?: #CandyRoute

	// --- services / volumes / aliases / extract / data ---
	service?: [...#CandyService]
	volume?: [...#CandyVolume]
	alias?: [...#CandyAlias]
	extract?: [...#CandyExtract]
	data?: [...#CandyData]

	// --- security / libvirt / hooks ---
	security?: #Security
	libvirt?: [...(string & !="")]
	hook?: #CandyHook

	// --- env/secret/mcp dependency + provides surface ---
	env_provide?: #StrMap
	env_require?: [...#CandyEnvDep]
	env_accept?: [...#CandyEnvDep]
	secret_accept?: [...#CandySecretDep]
	secret_require?: [...#CandySecretDep]
	mcp_provide?: [...#CandyMCPProvide]
	mcp_require?: [...#CandyMCPDep]
	mcp_accept?: [...#CandyMCPDep]
	secret?: [...#CandySecret]

	// --- shell init (same shape as box shell) ---
	shell?: #Shell

	// --- operator-facing artifacts ---
	artifact?: [...#CandyArtifact]
}

// ---------------------------------------------------------------------------
// Candy sub-shapes (#Candy-prefixed to avoid cross-kind collisions). Each CLOSED.
// ---------------------------------------------------------------------------

// ServiceEntry (service_render.go). use_packaged XOR exec is a Go cross-field
// rule (mixed-entry polymorphism allows the SAME name twice: one packaged form,
// one exec form) — neither is required at the CUE layer.
#CandyService: {
	name:          string & !=""
	use_packaged?: string & !=""
	exec?:         string & !=""
	env?: #StrMap
	restart?:           "no" | "on-failure" | "always" | "unless-stopped"
	working_directory?: string & !=""
	user?:              string & !=""
	after?: [...(string & !="")]
	before?: [...(string & !="")]
	wanted_by?: [...(string & !="")]
	stdout?:       string & !="" // "journal" | "none" | "file:<path>"
	stop_timeout?: (string & !="") | int // "20s" or an unquoted 20 (Go-coerced)
	scope?:        "system" | "user"
	enable?:       bool
	overrides?:    #CandyServiceOverrides
	kind?:         "program" | "eventlistener"
	event?:        string & !=""
	auto_start?:   bool
	start_retry?:  int & >=0
	start_sec?:    int & >=0
	stop_signal?:  string & !=""
	exit_code?:    string & !=""
	priority?:     int
}
#CandyServiceOverrides: {
	env?: #StrMap
	after?: [...(string & !="")]
	exec?: string & !=""
}

// VolumeYAML — name is lowercase-hyphen (validateVolume); path may be
// home-relative (~/…) or absolute, so not anchored to /.
#CandyVolume: {
	name: string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	path: string & !=""
}

// AliasYAML — for a CANDY alias `command` is REQUIRED (validateAliases); the
// box-level #BoxAlias keeps it optional (defaults to name).
#CandyAlias: {
	name:    string & !=""
	command: string & !=""
}

// ExtractYAML — copy a path out of another OCI image into this one. source is
// an image ref; path (in the source image) and dest (in this image) are
// absolute (validateCandyContents).
#CandyExtract: {
	source: string & !=""
	path:   string & =~"^/"
	dest:   string & =~"^/"
}

// DataYAML — stage candy-dir data into a volume at build, provision at deploy.
#CandyData: {
	src:    string & !=""
	volume: string & !=""
	dest?:  string & !=""
}

// EnvDependency for env_require/env_accept — name must be a valid env var name
// (isValidEnvVarName), description is mandatory (validate.go).
#CandyEnvDep: {
	name:        string & =~"^[A-Za-z_][A-Za-z0-9_]*$"
	description: string & !=""
	default?:    string
}

// EnvDependency for secret_accept/secret_require — adds the optional credential
// store key override, which must be "<service>/<key>" under the charly/ prefix.
#CandySecretDep: {
	#CandyEnvDep
	key?: string & =~"^charly/[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9_-]*$"
}

// EnvDependency for mcp_require/mcp_accept — the name is an MCP server name
// (lowercase-hyphen, NOT env-var-constrained); description mandatory.
#CandyMCPDep: {
	name:        string & !=""
	description: string & !=""
}

// MCPServerYAML — mcp_provide entry exposed to peer containers.
#CandyMCPProvide: {
	name:       string & !=""
	url:        string & !=""
	transport?: *"http" | "sse"
}

// SecretYAML — a candy-owned secret (target defaults to /run/secrets/<name>).
#CandySecret: {
	name:    string & !=""
	target?: string & !=""
	env?:    string & !=""
}

// HooksConfig — lifecycle hook scripts.
#CandyHook: {
	post_enable?: string & !=""
	pre_remove?:  string & !=""
}

// CandyArtifact — a file the candy publishes back to the operator post-setup.
#CandyArtifact: {
	name:        string & !=""
	path:        string & !=""
	retrieve_to: string & !=""
	mode?:       string & =~"^0[0-7]{3,4}$"
	optional?:   bool
	wait_second?: int & >=0
	rewrite?: [...#CandyArtifactRewrite]
}
#CandyArtifactRewrite: {
	find:     string & !=""
	replace?: string
}

// CandyCapabilities — image-level facts a candy contributes (aggregated at
// resolve time). oci_label is a genuine open string→string passthrough.
#CandyCapability: {
	preserve_user?:         bool
	needs_root_after_init?: bool
	init_system_hint?:      string & !=""
	data_only?:             bool
	oci_label?: {[string]: string}
}

// ApkPackageSpec — one Android app install. package (apkeep by id) XOR apk
// (committed local APK path) — exactly one.
#CandyApk: {
	package?:     string & !=""
	apk?:         string & !=""
	source?:      *"apk-pure" | "google-play" | "f-droid" | "huawei-app-gallery"
	arch?:        string & !=""
	app_version?: string & !=""
	// exactly one of package/apk (disjunction keeps it CLOSED; matchN would open it).
} & ({package!: _, apk?: _|_} | {apk!: _, package?: _|_})

// RouteYAML — generic service-route metadata (traefik / tunnel).
#CandyRoute: {
	host: string & !=""
	port: int & >0 & <=65535
}
