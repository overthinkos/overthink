// Shared CUE definitions referenced by multiple kinds. R3: define each shared
// shape ONCE here, not per-kind. All schema/*.cue files compile into one
// instance (no package clauses), so any kind def can reference these directly.

// Execution context for a plan step.
#Context: ("build" | "deploy" | "runtime") @go(-)

// #OpVerb — the verb DISCRIMINATOR vocabulary: the subset of #Op fields that are
// VERBS (exactly-one-set discriminators), as opposed to the shared modifiers
// (to/mode/content/timeout/…) that decorate them. #Op alone cannot express the
// verb-vs-modifier split (every field is structurally a string/int), so this enum
// is the ONE CUE source naming the verbs — schemagen emits it as spec.OpVerbs
// (the canonical verb set for Op.Kind()'s exactly-one-verb error + the package-main
// VerbCatalog dispatch table, both gated against it). Every name MUST be an #Op
// field (the schemagen gate "OpVerbs ⊆ AuthoringVerbs" proves it) and MUST have a
// VerbCatalog entry (the registry bijection gate proves it). Keep in lockstep with
// the `--- verb discriminators ---` group in #Op.
#OpVerb: ("mkdir" | "copy" | "write" | "link" | "download" | "setcap" | "build" |
	"command" | "file" | "package" | "service" | "port" | "process" | "http" |
	"dns" | "user" | "group" | "interface" | "kernel-param" | "mount" | "addr" |
	"matching" | "cdp" | "wl" | "dbus" | "vnc" | "mcp" | "record" | "spice" |
	"libvirt" | "k8s" | "adb" | "appium" | "summarize" | "kill" | "plugin") @go(-)

// ---------------------------------------------------------------------------
// Plan steps: the unified run/check/agent-run/agent-check/include vocabulary.
// ---------------------------------------------------------------------------

// One flat plan step: exactly ONE intent keyword (run / check / agent-run /
// agent-check / include) carrying prose, plus an inline Op (verb + modifiers).
// Exactly-one-keyword is the discriminated-union idiom — each arm requires its
// keyword and forbids the other four via _|_. Each arm EMBEDS the closed #Op,
// so the Op modifier field set is now CLOSED too (an unknown key is a typo).
// Op verb-exclusivity (Kind()) stays a Go per-entity check; CUE closes the
// field set, types every field, and enumerates every live-verb method.
// Every arm also forbids the runtime-derived venue/intent_do (stamped post-decode,
// never authored — see #Op).
#Step:
	{#Op, run: string & !="", check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, check: string & !="", run?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, "agent-run": string & !="", run?: _|_, check?: _|_, "agent-check"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, "agent-check": string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, include?: _|_, venue?: _|_, intent_do?: _|_} |
	{#Op, include: string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, venue?: _|_, intent_do?: _|_} @go(-) // gengotypes: hand-written Step (spec/union_types.go) embeds the generated Op

// #Op is the unified operation vocabulary (Go: charly/checkspec.go Op). CLOSED:
// every authored yaml key is modeled — an unknown key is a typo. Verb
// discriminators are optional (exactly-one is Go's Kind(), a cross-field rule
// kept in Go); CUE closes the set, types each field, and enumerates the
// live-verb methods + matcher operators. yaml:"-" fields (intentDo, origin) are
// never authored and intentionally absent.
#Op: {
	// --- verb discriminators (exactly one set; Go Kind() enforces) ---
	file?:           string
	package?:        string
	service?:        string
	port?:           int & >0 & <=65535 @go(,type=int)
	process?:        string
	command?:        string
	http?:           string @go(HTTP)
	dns?:            string @go(DNS)
	user?:           string
	group?:          string
	interface?:      string
	"kernel-param"?: string @go(KernelParam)
	mount?:          string
	addr?:           string
	matching?:       _ // verb value is any (scalar/list/map)
	mkdir?:          string
	copy?:           string
	write?:          string
	link?:           string
	download?:       string
	setcap?:         string
	build?:          string
	cdp?:            #CdpMethod
	wl?:             #WlMethod
	dbus?:           #DbusMethod
	vnc?:            #VncMethod
	mcp?:            #McpMethod
	record?:         #RecordMethod
	spice?:          #SpiceMethod
	libvirt?:        #LibvirtMethod
	k8s?:            #K8sMethod
	adb?:            #AdbMethod
	appium?:         #AppiumMethod
	summarize?:      string
	kill?:           string
	signal?:         "TERM" | "KILL"
	// plugin — the generic PLUGIN-VERB discriminator. Its value is a reserved word
	// served by a registered Provider (built-in or out-of-tree plugin); the host
	// #Op cannot type per-plugin verb fields (an external plugin's vocabulary is
	// not compiled in), so a plugin verb is authored via this generic envelope and
	// dispatched through providerRegistry.ResolveVerb. plugin_input carries the
	// params, validated by the PLUGIN's own spliced CUE schema (not base #Op).
	plugin?: string

	// --- shared resource-identity modifiers ---
	name?:      string
	namespace?: string
	label?:     string
	cluster?:   string
	manifest?:  string

	// --- k8s-specific modifiers ---
	k8s_kind?:     string @go(K8sKind)
	k8s_context?:  string @go(K8sContext)
	kubeconfig?:   string
	k8s_count?:    int    @go(K8sCount,type=int)
	k8s_resource?: string @go(K8sResource)
	k8s_group?:    string @go(K8sGroup)
	k8s_version?:  string @go(K8sVersion)
	json?:         bool   @go(JSON)

	// --- shared modifiers ---
	id?:           string @go(ID)
	description?:  string
	skip?:         bool
	timeout?:      #Duration
	in_container?: bool @go(InContainer,type=*bool)
	context?: [...#Context]
	// `pod:` (per-step container venue) is RETIRED — a step's execution venue is
	// derived ENTIRELY from its position in the bundle tree (flattenBundleVenues
	// → Op.venue, yaml:"-"). Authoring it is a closed-schema rejection (run:
	// charly migrate).
	depends_on?: [...string] @go(DependsOn)
	// plugin_input — generic params for a `plugin:` verb. Opaque to base #Op
	// (accepts any shape); the plugin's own spliced CUE schema validates it.
	plugin_input?: {...} @go(PluginInput,type=map[string]any)

	// --- install/build modifiers ---
	run_as?:  string @go(RunAs)
	to?:      string
	content?: string
	extract?: "tar.gz" | "tar.xz" | "tar.zst" | "zip" | "none" | "sh" | ""
	extract_include?: [...string] @go(ExtractInclude)
	strip_components?: int & >=0 @go(StripComponents,type=int)
	uninstall?: [...string]
	comment?: string
	cache?: [...string]
	env?: #StrMap

	// --- step modifiers ---
	capture?:         string
	capture_extract?: string @go(CaptureExtract)
	eventually?:      #Duration
	retry_interval?:  #Duration @go(RetryInterval)
	// `on:` (cross-member driver dispatch) is RETIRED — a step that drives a
	// peer/driver is authored as a step CHILD of that member node; its venue is
	// derived from position (flattenBundleVenues → Op.venue). Authoring `on:` is
	// a closed-schema rejection (run: charly migrate).
	tag?: [...string]

	// --- concurrency ---
	parallel?:   string
	count?:      int & >=0 @go(,type=int)
	index_var?:  string    @go(IndexVar)
	background?: bool

	// --- aggregation (summarize) ---
	over_id?: [...string] @go(OverIDs)
	metric?: [...string] @go(Metrics)
	emit_id?: string   @go(EmitID)
	p50?:     #Matcher @go(P50Match)
	p95?:     #Matcher @go(P95Match)
	p99?:     #Matcher @go(P99Match)
	max?:     #Matcher @go(MaxMatch)
	mean?:    #Matcher @go(MeanMatch)

	// Origin is populated at collection time (candy:<name>/box:<name>/deploy-*),
	// NEVER authored (Go yaml:"-"), but it TRAVELS in the ai.opencharly.description
	// OCI-label JSON (Go json:"origin") — so the generated spec.Op MUST carry it
	// for a faithful drop-in (do NOT @go(-) it). gengotypes emits
	// `json:"origin,omitempty"`, matching the hand tag.
	origin?: string @go(Origin)

	// venue + intent_do are RUNTIME-DERIVED, never authored: venue is stamped
	// from a step's bundle-tree position (flattenBundleVenues), intent_do from
	// the enclosing Step's keyword (run→act / check→assert / agent→instruct).
	// They are generated onto the Go struct (the check runner persists them on
	// the Op-by-value passed to runOne / EffectiveDo), but the #Step authoring
	// arms forbid them (`venue?: _|_, intent_do?: _|_`) so authoring either is a
	// closed-schema rejection — exactly the contract the retired `pod:`/`on:`
	// fields enforced. yaml:"-" (never read from YAML); json omits them when
	// empty (the bake-time state, so they never leak into the description label).
	venue?:     string @go(Venue)
	intent_do?: string @go(IntentDo)

	// --- file ---
	exists?:   bool @go(,type=*bool)
	mode?:     string & =~"^0[0-7]{3,4}$"
	owner?:    string
	group_of?: string @go(GroupOf)
	filetype?: "file" | "directory" | "symlink"
	contains?: #ContainsList
	sha256?:   string

	// --- package ---
	installed?: bool @go(,type=*bool)
	version?: [...string] @go(Versions)
	package_map?: {[string]: string} @go(PackageMap)
	exclude_distro?: [...string] @go(ExcludeDistros)

	// --- service ---
	enabled?: bool @go(,type=*bool)
	running?: bool @go(,type=*bool)

	// --- port ---
	listening?: bool   @go(,type=*bool)
	ip?:        string @go(IP)

	// --- command ---
	exit_status?: int @go(ExitStatus,type=*int)
	stdout?:      #MatcherList
	stderr?:      #MatcherList
	from_host?:   bool @go(FromHost)

	// --- http ---
	status?:              int & >=100 & <600 @go(,type=int)
	body?:                #MatcherList
	header?:              #MatcherList @go(Headers)
	allow_insecure?:      bool         @go(AllowInsecure)
	no_follow_redirects?: bool         @go(NoFollowRedir)
	ca_file?:             string       @go(CAFile)
	method?:              string
	request_body?:        string @go(RequestBody)

	// --- dns ---
	resolvable?: bool @go(,type=*bool)
	addrs?: [...string]
	server?: string

	// --- user / group ---
	uid?:   int & >=0 @go(UID,type=*int)
	gid?:   int & >=0 @go(GID,type=*int)
	home?:  string
	shell?: string
	groups?: [...string]

	// --- interface ---
	mtu?: int @go(MTU,type=*int)

	// --- kernel-param / mount ---
	value?:        #MatcherList
	mount_source?: string @go(MountSource)
	filesystem?:   string
	opt?:          #MatcherList @go(Opts)

	// --- addr ---
	reachable?: bool @go(,type=*bool)

	// --- cdp / wl / dbus / vnc / spice modifiers ---
	tab?:        string
	expression?: string
	url?:        string @go(URL)
	selector?:   string
	dest?:       string
	path?:       string
	arg?: [...string] @go(Args)
	artifact?:                 string
	artifact_min_bytes?:       int & >=0                    @go(ArtifactMinBytes,type=int)
	artifact_min_dimensions?:  string & =~"^[0-9]+x[0-9]+$" @go(ArtifactMinDimensions)
	artifact_not_uniform?:     bool                         @go(ArtifactNotUniform)
	artifact_min_cast_events?: int & >=0                    @go(ArtifactMinCastEvents,type=int)
	x?:                        int                          @go(,type=int)
	y?:                        int                          @go(,type=int)
	x2?:                       int                          @go(,type=int)
	y2?:                       int                          @go(,type=int)
	button?:                   string
	text?:                     string
	key?:                      string @go(KeyName)
	combo?:                    string
	direction?:                string
	amount?:                   int @go(,type=int)
	target?:                   string
	action?:                   string
	query?:                    string

	// --- mcp ---
	mcp_name?: string @go(McpName)
	tool?:     string
	uri?:      string @go(URI)
	input?:    string

	// --- adb / appium ---
	apk?:         string
	property?:    string
	caps?:        string
	strategy?:    string
	session?:     string
	app_id?:      string @go(AppId)
	source?:      string
	arch?:        string
	app_version?: string @go(AppVersion)
	activity?:    string
	attribute?:   string
	percent?:     string
	keycode?:     int @go(,type=int)
	params?:      string

	// --- record ---
	record_name?:  string    @go(RecordName)
	record_mode?:  string    @go(RecordMode)
	record_fps?:   int & >=0 @go(RecordFps,type=int)
	record_audio?: bool      @go(RecordAudio)
}

// Live-container verb method allowlists — exact mirrors of the Go maps in
// checkrun_charly_verbs.go (cdpMethods/wlMethods/…). A method outside the set
// is rejected (validateCharlyVerb parity, now enforced declaratively in CUE).
#CdpMethod:     ("status" | "list" | "url" | "text" | "html" | "eval" | "axtree" | "coords" | "raw" | "wait" | "screenshot" | "open" | "close" | "click" | "type" | "spa-status" | "spa-click" | "spa-type" | "spa-key" | "spa-key-combo" | "spa-mouse") @go(-)
#WlMethod:      "status" | "toplevel" | "windows" | "geometry" | "xprop" | "atspi" | "screenshot" | "clipboard" | "click" | "double-click" | "mouse" | "scroll" | "drag" | "type" | "key" | "key-combo" | "focus" | "close" | "fullscreen" | "minimize" | "exec" | "resolution" | "overlay-list" | "overlay-status" | "overlay-show" | "overlay-hide" | "sway-tree" | "sway-workspaces" | "sway-outputs" | "sway-msg" | "sway-focus" | "sway-move" | "sway-resize" | "sway-layout" | "sway-workspace" | "sway-kill" | "sway-floating" | "sway-reload" @go(-)
#DbusMethod:    "list" | "call" | "introspect" | "notify" @go(-)
#VncMethod:     "status" | "screenshot" | "click" | "mouse" | "type" | "key" | "rfb" | "passwd" @go(-)
#McpMethod:     "ping" | "servers" | "list-tools" | "list-resources" | "list-prompts" | "call" | "read" @go(-)
#RecordMethod:  "list" | "start" | "stop" | "cmd" @go(-)
#SpiceMethod:   "status" | "screenshot" | "cursor" | "click" | "mouse" | "type" | "key" @go(-)
#LibvirtMethod: "list" | "info" | "screenshot" | "send-key" | "passwd" | "qmp" | "domain-xml" | "console" | "events" | "guest/ping" | "guest/info" | "guest/os-info" | "guest/time" | "guest/hostname" | "guest/users" | "guest/interfaces" | "guest/disks" | "guest/fsinfo" | "guest/vcpus" | "guest/exec" | "guest/fstrim" | "snapshot/list" | "snapshot/create" | "snapshot/info" | "snapshot/revert" | "snapshot/delete" @go(-)
#K8sMethod:     "nodes" | "wait-nodes" | "pods" | "wait-ready" | "ingress" | "ingressclass" | "storageclass" | "service" | "lb-external-ip" | "addons" | "apply" | "delete" | "raw" @go(-)
#AdbMethod:     "devices" | "shell" | "install" | "install-app" | "uninstall" | "getprop" | "screencap" | "logcat-tail" | "wait-for-device" | "wait-ui-settled" | "current-focus" | "keyevent" @go(-)
#AppiumMethod:  "status" | "session-create" | "session-delete" | "install-app" | "find" | "click" | "send-keys" | "screenshot" | "get-text" | "get-attribute" | "clear" | "find-all" | "source" | "back" | "gesture-tap" | "gesture-double-tap" | "gesture-long-press" | "gesture-drag" | "gesture-swipe" | "gesture-scroll" | "gesture-fling" | "gesture-pinch-open" | "gesture-pinch-close" | "app-start-activity" | "app-activate" | "app-terminate" | "app-remove" | "app-clear" | "app-is-installed" | "app-state" | "app-current-activity" | "app-current-package" | "key-press" | "key-hide" | "key-shown" | "device-info" | "device-battery" | "device-time" | "device-orientation" | "device-set-orientation" | "device-notifications" | "device-get-clipboard" | "device-set-clipboard" | "device-contexts" | "device-context" | "execute" | "raw" @go(-)

// Matcher operators (validMatcherOps, validate_check.go). A matcher is a scalar
// (implicit equals), or a single-operator map; #MatcherList accepts a single
// matcher OR a list. #ContainsList shares the shape (bare scalars mean contains
// at decode, but the validated SHAPE is identical).
#MatchOpMap: {equals: _} | {not_equals: _} | {contains: _} | {not_contains: _} | {matches: _} | {not_matches: _} | {lt: _} | {le: _} | {gt: _} | {ge: _}

#Matcher: (string | bool | number | #MatchOpMap) @go(-) // gengotypes: hand Matcher (spec/union_types.go, ported from checkspec.go)

#MatcherList: (#Matcher | [...#Matcher]) @go(-) // gengotypes: hand MatcherList

#ContainsList: (#MatcherList) @go(-) // gengotypes: hand ContainsList

// ---------------------------------------------------------------------------
// Build-vocabulary shared shapes (distro formats + builders).
// ---------------------------------------------------------------------------

// A BuildKit cache mount. dst is the absolute in-builder cache path; sharing is
// the BuildKit sharing mode; owned renders a uid/gid-owned cache.
#CacheMount: {
	dst:      string & =~"^/"
	sharing?: *"locked" | "shared" | "private"
	owned?:   bool
}

// Three-phase template set: prepare → install → cleanup, each with a container
// (Containerfile) and host (shell) rendering. Template bodies are Go
// text/template — strings, never parsed here.
#PhaseSet: {
	prepare?: #PhaseTemplates @go(Prepare,optional=nillable)
	install?: #PhaseTemplates @go(Install,optional=nillable)
	cleanup?: #PhaseTemplates @go(Cleanup,optional=nillable)
}

#PhaseTemplates: {
	container?: string
	host?:      string
}

// ---------------------------------------------------------------------------
// Cross-kind scalar/ref patterns (R3: one home, referenced everywhere).
// ---------------------------------------------------------------------------

// CalVer schema/entity version: YYYY.DDD.HHMM.
#CalVer: string & =~"^[0-9]{4}\\.[0-9]{1,3}\\.[0-9]{3,4}$" @go(-)

// A lowercase-hyphenated entity name / cross-ref.
#EntityRef: string & =~"^[a-z0-9]+(-[a-z0-9]+)*$" @go(-)

// A candy ref: bare lowercase-hyphenated name OR a remote @github…:vTAG ref.
#CandyRef: string & =~"^(@.+|[a-z0-9]+(-[a-z0-9]+)*)$" @go(-)

// A host:container port pin (optionally IP-prefixed).
#PortPin: string & =~"^(\\[[0-9a-fA-F:]+\\]:|[0-9]{1,3}(\\.[0-9]{1,3}){3}:)?[0-9]+:[0-9]+$" @go(-)

// A memory/disk size, e.g. "2G", "8192M", "6.5Gi".
#VmSize: string & =~"^[0-9]+(\\.[0-9]+)?([KMGTP]i?B?)?$" @go(-)

// A container-resource size (SecurityConfig shm_size/memory_*).
#Size: string & =~"^[0-9]+(\\.[0-9]+)?[kKmMgG]?$" @go(-)

// An env entry: KEY=VALUE (value may be empty / contain =).
#EnvVar: string & =~"^[A-Za-z_][A-Za-z0-9_]*=.*$" @go(-)

// A YAML scalar Go decodes into a string (map[string]string / string field):
// yaml.v3 coerces an unquoted int/bool/float to its literal text, so an
// idiomatic `PORT: 8080` is a valid string value. #StrMap is the matching map.
#StrVal: string | number | bool // gengotypes: emits `type StrVal any` — referenced by inline {[string]: #StrVal} maps

#StrMap: ({[string]: #StrVal}) @go(-) // gengotypes: hand StrMap = map[string]string (spec/union_types.go)

// Go time.ParseDuration string (units ns/us/µs/ms/s/m/h), e.g. "30m", "1h30m".
#Duration: string & =~"^[0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h)([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$" @go(-)

// ---------------------------------------------------------------------------
// Container security (SecurityConfig) — shared by box / candy / deploy / sidecar.
// ---------------------------------------------------------------------------
#Security: {
	privileged?: bool
	cgroupns?:   "host" | "private" | "" @go(CgroupNS)
	cap_add?: [...string] @go(CapAdd)
	devices?: [...string]
	security_opt?: [...string] @go(SecurityOpt)
	ipc_mode?: "host" | "private" | "shareable" | "" @go(IpcMode)
	shm_size?: #Size                                 @go(ShmSize)
	group_add?: [...string] @go(GroupAdd)
	mount?: [...string] @go(Mounts)
	memory_max?:      #Size @go(MemoryMax)
	memory_high?:     #Size @go(MemoryHigh)
	memory_swap_max?: #Size @go(MemorySwapMax)
	cpus?:            string & =~"^[0-9]+(\\.[0-9]+)?$"
}

// ---------------------------------------------------------------------------
// Shell-rc config (ShellConfig/ShellSpec) — shared by box + candy. CLOSED: the
// Go UnmarshalYAML rejects any key outside the 4 intrinsic + 4 shell names.
// ---------------------------------------------------------------------------
#Shell: {
	init?: string
	path_append?: [...string] @go(PathAppend)
	path?:     string
	priority?: int @go(,type=int)
	bash?:     #ShellSpec @go(Bash,optional=nillable)
	zsh?:      #ShellSpec @go(Zsh,optional=nillable)
	fish?:     #ShellSpec @go(Fish,optional=nillable)
	sh?:       #ShellSpec @go(Sh,optional=nillable)
}
#ShellSpec: {
	init?: string
	path_append?: [...string] @go(PathAppend)
	path?: string
}

// ---------------------------------------------------------------------------
// Calamares package surface (PackageItem/DistroPackages/AURPackages) — shared by
// candy + group. `repo` is the genuine typed-open passthrough (#RepoBlock).
// ---------------------------------------------------------------------------
// bare scalar shorthand XOR object form.
#PackageItem: ((string & !="") | {
		name:         string & !=""
		description?: string & !=""
}) @go(-) // gengotypes: hand PackageItem (spec/union_types.go)
#DistroPackages: {
	package?: [...#PackageItem]
	copr?: [...(string & !="")]
	repo?: [...#RepoBlock]
	exclude?: [...(string & !="")]
	option?: [...(string & !="")] @go(Options)
	module?: [...(string & !="")]
	aur?: #AUR @go(AUR,optional=nillable)
}
#AUR: {
	package?: [...#PackageItem]
	option?: [...(string & !="")] @go(Options)
	replace?: [...(string & !="")] @go(Replaces)
}

// Free-form per-distro upstream-repo block: `name` is load-bearing (the only
// key any code requires), the rest pass through verbatim to install templates.
#RepoBlock: {
	name: string & !=""
	...
} @go(-) // gengotypes: hand-written `type RepoBlock = map[string]any` (spec/scalar_aliases.go) — an ALIAS so []RepoBlock IS []map[string]any (toMapSlice / template rendering)

// install_opts gates (deploy.go InstallOptsConfig) — shared by local + deploy.
#InstallOpts: {
	with_service?:       bool @go(WithServices)
	allow_repo_changes?: bool @go(AllowRepoChanges)
	allow_root_tasks?:   bool @go(AllowRootTasks)
	skip_incompatible?:  bool @go(SkipIncompatible)
	verify?:             bool
	builder_image?:      string & !="" @go(BuilderImage)
}
