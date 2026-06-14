// Shared CUE definitions referenced by multiple kinds. R3: define each shared
// shape ONCE here, not per-kind. All schema/*.cue files compile into one
// instance (no package clauses), so any kind def can reference these directly.

// Execution context for a plan step.
#Context: "build" | "deploy" | "runtime"

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
#Step:
	{#Op, run: string & !="", check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_} |
	{#Op, check: string & !="", run?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_} |
	{#Op, "agent-run": string & !="", run?: _|_, check?: _|_, "agent-check"?: _|_, include?: _|_} |
	{#Op, "agent-check": string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, include?: _|_} |
	{#Op, include: string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_}

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
	port?:           int & >0 & <=65535
	process?:        string
	command?:        string
	http?:           string
	dns?:            string
	user?:           string
	group?:          string
	interface?:      string
	"kernel-param"?: string
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

	// --- shared resource-identity modifiers ---
	name?:      string
	namespace?: string
	label?:     string
	cluster?:   string
	manifest?:  string

	// --- k8s-specific modifiers ---
	k8s_kind?:     string
	k8s_context?:  string
	kubeconfig?:   string
	k8s_count?:    int
	k8s_resource?: string
	k8s_group?:    string
	k8s_version?:  string
	json?:         bool

	// --- shared modifiers ---
	id?:           string
	description?:  string
	skip?:         bool
	timeout?:      #Duration
	in_container?: bool
	context?: [...#Context]
	pod?: string
	depends_on?: [...string]

	// --- install/build modifiers ---
	run_as?:          string
	to?:              string
	content?:         string
	extract?:         "tar.gz" | "tar.xz" | "tar.zst" | "zip" | "none" | "sh" | ""
	extract_include?: [...string]
	strip_components?: int & >=0
	uninstall?: [...string]
	comment?: string
	cache?: [...string]
	env?: #StrMap

	// --- step modifiers ---
	capture?:         string
	capture_extract?: string
	eventually?:      #Duration
	retry_interval?:  #Duration
	on?:              string
	tag?: [...string]

	// --- concurrency ---
	parallel?:   string
	count?:      int & >=0
	index_var?:  string
	background?: bool

	// --- aggregation (summarize) ---
	over_id?: [...string]
	metric?: [...string]
	emit_id?: string
	p50?:     #Matcher
	p95?:     #Matcher
	p99?:     #Matcher
	max?:     #Matcher
	mean?:    #Matcher

	// --- file ---
	exists?:   bool
	mode?:     string & =~"^0[0-7]{3,4}$"
	owner?:    string
	group_of?: string
	filetype?: "file" | "directory" | "symlink"
	contains?: #ContainsList
	sha256?:   string

	// --- package ---
	installed?: bool
	version?: [...string]
	package_map?: {[string]: string}
	exclude_distro?: [...string]

	// --- service ---
	enabled?: bool
	running?: bool

	// --- port ---
	listening?: bool
	ip?:        string

	// --- command ---
	exit_status?: int
	stdout?:      #MatcherList
	stderr?:      #MatcherList
	from_host?:   bool

	// --- http ---
	status?:              int & >=100 & <600
	body?:                #MatcherList
	header?:              #MatcherList
	allow_insecure?:      bool
	no_follow_redirects?: bool
	ca_file?:             string
	method?:              string
	request_body?:        string

	// --- dns ---
	resolvable?: bool
	addrs?: [...string]
	server?: string

	// --- user / group ---
	uid?:  int & >=0
	gid?:  int & >=0
	home?: string
	shell?: string
	groups?: [...string]

	// --- interface ---
	mtu?: int

	// --- kernel-param / mount ---
	value?:        #MatcherList
	mount_source?: string
	filesystem?:   string
	opt?:          #MatcherList

	// --- addr ---
	reachable?: bool

	// --- cdp / wl / dbus / vnc / spice modifiers ---
	tab?:                      string
	expression?:               string
	url?:                      string
	selector?:                 string
	dest?:                     string
	path?:                     string
	arg?: [...string]
	artifact?:                 string
	artifact_min_bytes?:       int & >=0
	artifact_min_dimensions?:  string & =~"^[0-9]+x[0-9]+$"
	artifact_not_uniform?:     bool
	artifact_min_cast_events?: int & >=0
	x?:                        int
	y?:                        int
	x2?:                       int
	y2?:                       int
	button?:                   string
	text?:                     string
	key?:                      string
	combo?:                    string
	direction?:                string
	amount?:                   int
	target?:                   string
	action?:                   string
	query?:                    string

	// --- mcp ---
	mcp_name?: string
	tool?:     string
	uri?:      string
	input?:    string

	// --- adb / appium ---
	apk?:         string
	property?:    string
	caps?:        string
	strategy?:    string
	session?:     string
	app_id?:      string
	source?:      string
	arch?:        string
	app_version?: string
	activity?:    string
	attribute?:   string
	percent?:     string
	keycode?:     int
	params?:      string

	// --- record ---
	record_name?:  string
	record_mode?:  string
	record_fps?:   int & >=0
	record_audio?: bool
}

// Live-container verb method allowlists — exact mirrors of the Go maps in
// checkrun_charly_verbs.go (cdpMethods/wlMethods/…). A method outside the set
// is rejected (validateCharlyVerb parity, now enforced declaratively in CUE).
#CdpMethod: "status" | "list" | "url" | "text" | "html" | "eval" | "axtree" | "coords" | "raw" | "wait" | "screenshot" | "open" | "close" | "click" | "type" | "spa-status" | "spa-click" | "spa-type" | "spa-key" | "spa-key-combo" | "spa-mouse"
#WlMethod: "status" | "toplevel" | "windows" | "geometry" | "xprop" | "atspi" | "screenshot" | "clipboard" | "click" | "double-click" | "mouse" | "scroll" | "drag" | "type" | "key" | "key-combo" | "focus" | "close" | "fullscreen" | "minimize" | "exec" | "resolution" | "overlay-list" | "overlay-status" | "overlay-show" | "overlay-hide" | "sway-tree" | "sway-workspaces" | "sway-outputs" | "sway-msg" | "sway-focus" | "sway-move" | "sway-resize" | "sway-layout" | "sway-workspace" | "sway-kill" | "sway-floating" | "sway-reload"
#DbusMethod: "list" | "call" | "introspect" | "notify"
#VncMethod: "status" | "screenshot" | "click" | "mouse" | "type" | "key" | "rfb" | "passwd"
#McpMethod: "ping" | "servers" | "list-tools" | "list-resources" | "list-prompts" | "call" | "read"
#RecordMethod: "list" | "start" | "stop" | "cmd"
#SpiceMethod: "status" | "screenshot" | "cursor" | "click" | "mouse" | "type" | "key"
#LibvirtMethod: "list" | "info" | "screenshot" | "send-key" | "passwd" | "qmp" | "domain-xml" | "console" | "events" | "guest/ping" | "guest/info" | "guest/os-info" | "guest/time" | "guest/hostname" | "guest/users" | "guest/interfaces" | "guest/disks" | "guest/fsinfo" | "guest/vcpus" | "guest/exec" | "guest/fstrim" | "snapshot/list" | "snapshot/create" | "snapshot/info" | "snapshot/revert" | "snapshot/delete"
#K8sMethod: "nodes" | "wait-nodes" | "pods" | "wait-ready" | "ingress" | "ingressclass" | "storageclass" | "service" | "lb-external-ip" | "addons" | "apply" | "delete" | "raw"
#AdbMethod: "devices" | "shell" | "install" | "install-app" | "uninstall" | "getprop" | "screencap" | "logcat-tail" | "wait-for-device" | "wait-ui-settled" | "current-focus" | "keyevent"
#AppiumMethod: "status" | "session-create" | "session-delete" | "install-app" | "find" | "click" | "send-keys" | "screenshot" | "get-text" | "get-attribute" | "clear" | "find-all" | "source" | "back" | "gesture-tap" | "gesture-double-tap" | "gesture-long-press" | "gesture-drag" | "gesture-swipe" | "gesture-scroll" | "gesture-fling" | "gesture-pinch-open" | "gesture-pinch-close" | "app-start-activity" | "app-activate" | "app-terminate" | "app-remove" | "app-clear" | "app-is-installed" | "app-state" | "app-current-activity" | "app-current-package" | "key-press" | "key-hide" | "key-shown" | "device-info" | "device-battery" | "device-time" | "device-orientation" | "device-set-orientation" | "device-notifications" | "device-get-clipboard" | "device-set-clipboard" | "device-contexts" | "device-context" | "execute" | "raw"

// Matcher operators (validMatcherOps, validate_check.go). A matcher is a scalar
// (implicit equals), or a single-operator map; #MatcherList accepts a single
// matcher OR a list. #ContainsList shares the shape (bare scalars mean contains
// at decode, but the validated SHAPE is identical).
#MatchOpMap: {equals: _} | {not_equals: _} | {contains: _} | {not_contains: _} | {matches: _} | {not_matches: _} | {lt: _} | {le: _} | {gt: _} | {ge: _}
#Matcher:    string | bool | number | #MatchOpMap
#MatcherList: #Matcher | [...#Matcher]
#ContainsList: #MatcherList

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
	prepare?: #PhaseTemplates
	install?: #PhaseTemplates
	cleanup?: #PhaseTemplates
}
#PhaseTemplates: {
	container?: string
	host?:      string
}

// ---------------------------------------------------------------------------
// Cross-kind scalar/ref patterns (R3: one home, referenced everywhere).
// ---------------------------------------------------------------------------

// CalVer schema/entity version: YYYY.DDD.HHMM.
#CalVer: string & =~"^[0-9]{4}\\.[0-9]{1,3}\\.[0-9]{3,4}$"
// A lowercase-hyphenated entity name / cross-ref.
#EntityRef: string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
// A candy ref: bare lowercase-hyphenated name OR a remote @github…:vTAG ref.
#CandyRef: string & =~"^(@.+|[a-z0-9]+(-[a-z0-9]+)*)$"
// A host:container port pin (optionally IP-prefixed).
#PortPin: string & =~"^(\\[[0-9a-fA-F:]+\\]:|[0-9]{1,3}(\\.[0-9]{1,3}){3}:)?[0-9]+:[0-9]+$"
// A memory/disk size, e.g. "2G", "8192M", "6.5Gi".
#VmSize: string & =~"^[0-9]+(\\.[0-9]+)?([KMGTP]i?B?)?$"
// A container-resource size (SecurityConfig shm_size/memory_*).
#Size: string & =~"^[0-9]+(\\.[0-9]+)?[kKmMgG]?$"
// An env entry: KEY=VALUE (value may be empty / contain =).
#EnvVar: string & =~"^[A-Za-z_][A-Za-z0-9_]*=.*$"
// A YAML scalar Go decodes into a string (map[string]string / string field):
// yaml.v3 coerces an unquoted int/bool/float to its literal text, so an
// idiomatic `PORT: 8080` is a valid string value. #StrMap is the matching map.
#StrVal: string | number | bool
#StrMap: {[string]: #StrVal}
// Go time.ParseDuration string (units ns/us/µs/ms/s/m/h), e.g. "30m", "1h30m".
#Duration: string & =~"^[0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h)([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$"

// ---------------------------------------------------------------------------
// Container security (SecurityConfig) — shared by box / candy / deploy / sidecar.
// ---------------------------------------------------------------------------
#Security: {
	privileged?: bool
	cgroupns?:   "host" | "private" | ""
	cap_add?: [...string]
	devices?: [...string]
	security_opt?: [...string]
	ipc_mode?: "host" | "private" | "shareable" | ""
	shm_size?: #Size
	group_add?: [...string]
	mount?: [...string]
	memory_max?:      #Size
	memory_high?:     #Size
	memory_swap_max?: #Size
	cpus?:            string & =~"^[0-9]+(\\.[0-9]+)?$"
}

// ---------------------------------------------------------------------------
// Shell-rc config (ShellConfig/ShellSpec) — shared by box + candy. CLOSED: the
// Go UnmarshalYAML rejects any key outside the 4 intrinsic + 4 shell names.
// ---------------------------------------------------------------------------
#Shell: {
	init?: string
	path_append?: [...string]
	path?:     string
	priority?: int
	bash?:     #ShellSpec
	zsh?:      #ShellSpec
	fish?:     #ShellSpec
	sh?:       #ShellSpec
}
#ShellSpec: {
	init?: string
	path_append?: [...string]
	path?: string
}

// ---------------------------------------------------------------------------
// Calamares package surface (PackageItem/DistroPackages/AURPackages) — shared by
// candy + group. `repo` is the genuine typed-open passthrough (#RepoBlock).
// ---------------------------------------------------------------------------
// bare scalar shorthand XOR object form.
#PackageItem: (string & !="") | {
	name:         string & !=""
	description?: string & !=""
}
#DistroPackages: {
	package?: [...#PackageItem]
	copr?: [...(string & !="")]
	repo?: [...#RepoBlock]
	exclude?: [...(string & !="")]
	option?: [...(string & !="")]
	module?: [...(string & !="")]
	aur?: #AUR
}
#AUR: {
	package?: [...#PackageItem]
	option?: [...(string & !="")]
	replace?: [...(string & !="")]
}
// Free-form per-distro upstream-repo block: `name` is load-bearing (the only
// key any code requires), the rest pass through verbatim to install templates.
#RepoBlock: {
	name: string & !=""
	...
}

// install_opts gates (deploy.go InstallOptsConfig) — shared by local + deploy.
#InstallOpts: {
	with_service?:       bool
	allow_repo_changes?: bool
	allow_root_tasks?:   bool
	skip_incompatible?:  bool
	verify?:             bool
	builder_image?:      string & !=""
}
