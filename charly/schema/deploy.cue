// CUE schema for the `deploy` AND `check` kinds. Both validate ONE
// DeploymentNode (charly/deploy.go): a `deploy:` map entry, or a `kind: check`
// bed (disposable:true + usually iterate:/plan:). #Deploy is the base node;
// #Check narrows it to the bed invariants. CLOSED. Shared defs REFERENCED, not
// redefined (R3): #Step/#Op/#Security/#EnvVar/#InstallOpts/#Duration/#CalVer/
// #EntityRef/#PortPin/#VmSize/#Sidecar/#ShellSpec live in _common.cue / sidecar.cue.

#Deploy: {
	version?:     #CalVer
	description?: string & !=""

	target: *"pod" | "vm" | "k8s" | "local" | "android"

	box?:     string & !=""
	pod?:     #EntityRef
	vm?:      #EntityRef
	k8s?:     #EntityRef
	local?:   #EntityRef
	android?: #EntityRef

	kind?:     "service" | "daemon" | "batch" | "scheduled" | "oneshot"
	replica?:  int & >=0
	restart?:  "always" | "on-failure" | "never"
	schedule?: string & !=""

	tunnel?:     #Tunnel
	dns?:        string & !=""
	acme_email?: string & !=""
	port?: [...#PortPin]
	resolved_port?: [...#PortPin]
	env?: [...#EnvVar]
	env_file?:          string & !=""
	network?:           string & !=""
	engine?:            "podman" | "docker"
	security?:          #Security
	secret?: [...#DeploySecret]
	volume?: [...#DeployVolume]
	sidecar?: {[string]: #Sidecar}
	forward_gpg_agent?: bool
	forward_ssh_agent?: bool

	plan?: [...#Step]
	iterate?: #Iterate
	shell?: [...#DeployShellOverlay]

	add_candy?: [...(string & !="")]
	install_opts?: #InstallOpts

	host?: string & !=""
	user?: string & !=""
	ssh_arg?: [...string]

	cpus?:      int & >=1
	ram?:       #VmSize
	disk_size?: #VmSize

	kubernetes?: #K8sDeploy

	resources?: #DeployResources
	expose?:    #DeployExpose
	storage?: [...#DeployStorage]
	probes?: #DeployProbes

	from_snapshot?:    string & !=""
	cloud_init_clean?: bool
	vm_state?: #VmDeployState

	disposable?: bool
	lifecycle?:  "scratch" | "dev" | "test" | "qa" | "staging" | "prod" | "custom"
	ephemeral?:  #Ephemeral
	preemptible?: #Preemptible
	requires_exclusive?: [...(string & !="")]
	requires_shared?: [...(string & !="")]

	// nested/peer map keys carry no dots (validateDeploymentName).
	nested?: {[=~"^[^.]+$"]: #Deploy}
	peer?: {[=~"^[^.]+$"]: #Deploy}
}

// #Check — a kind:check bed: iterate AI-benchmark (exempt from disposable) OR a
// deterministic R10 bed (disposable required, bed-legal target) OR an ephemeral
// bed (ephemeral ⇒ disposable). Each arm mutually forbids the other arms'
// discriminators via _|_ so the node collapses to exactly one arm under Concrete.
#Check: #Deploy & ({
	iterate:     #Iterate
	disposable?: _|_
	ephemeral?:  _|_
} | {
	disposable: true
	iterate?:   _|_
	ephemeral?: _|_
	target:     "pod" | "vm" | "local" | "android"
} | {
	ephemeral:   #Ephemeral
	iterate?:    _|_
	disposable?: _|_
	target:      "pod" | "vm" | "local" | "android"
})

#Tunnel: ("tailscale" | "cloudflare") | {
	provider: "tailscale" | "cloudflare"
	tunnel?:  string & !=""
	public?:  #PortScope
	private?: #PortScope
}
// PortScope (tunnel.go): "all" scalar | a list of container ports | a
// port→hostname map (PortMap). Ports are ints; hostnames strings.
#PortScope: "all" | [...int] | {[=~"^[0-9]+$"]: string}

// DeployShellOverlay (deploy.go) — per-deploy shell-rc overlay. CLOSED: the Go
// UnmarshalYAML allowlists exactly these keys + the 4 shell names.
#DeployShellOverlay: {
	id?:     string & !=""
	origin?: string & !=""
	skip?:   bool
	init?:   string
	path_append?: [...string]
	path?:     string
	priority?: int
	bash?:     #ShellSpec
	zsh?:      #ShellSpec
	fish?:     #ShellSpec
	sh?:       #ShellSpec
}

// DeployProbes (deploy.go) — each probe is an inline Op (the check verb vocab).
#DeployProbes: {
	liveness?:  #Op
	readiness?: #Op
	startup?:   #Op
}

// VmDeployState (deploy.go) — MACHINE-WRITTEN runtime state, never authored.
// Forward-evolving; open tail is the justified hatch for a state record.
#VmDeployState: {
	instance_id?:                 string
	disk_path?:                   string
	seed_iso?:                    string
	ssh_port?:                    int
	ssh_user?:                    string
	backend?:                     "qemu" | "libvirt"
	cloud_init_rendered_digest?:  string
	charly_install_strategy?:     "auto" | "scp" | "url" | "skip"
	...
}

#DeployVolume: {
	name:         string & !=""
	type?:        "volume" | "bind" | "encrypted"
	host?:        string & !=""
	path?:        string & =~"^/"
	data_seeded?: bool
	data_source?: string & !=""
}
#DeploySecret: {
	name:    string & !=""
	source?: string & !=""
}
#DeployResources: {
	cpu_request?:    string & !=""
	memory_request?: string & !=""
}
#DeployExpose: {
	host?: string & !=""
	path?: string & =~"^/"
	tls?:  bool
	port?: string & !=""
}
#DeployStorage: {
	name:        string & !=""
	size?:       string & !=""
	class_hint?: "fast" | "cheap" | "encrypted" | "default"
	access?:     "single-writer" | "many-readers" | "many-writers"
	path?:       string & =~"^/"
}
#K8sDeploy: {
	namespace?: #EntityRef
	workload?:  "Deployment" | "StatefulSet" | "DaemonSet" | "Pod" | "Job" | "CronJob"
	patches?: [...#K8sPatch]
	raw?: [...string]
}
#K8sPatch: {
	target: {
		kind?:      string & !=""
		name?:      string & !=""
		namespace?: string & !=""
	}
	patch: string & !=""
}
#Ephemeral: true | {
	ttl?:             #Duration
	keep_on_failure?: bool
	naming_pattern?:  string & !=""
}
#Preemptible: [...(string & !="")] | {
	holds?: [...(string & !="")]
	stop?:    "shutdown"
	restore?: "always" | "on-success"
}
#Iterate: {
	agent?: [...(string & !="")]
	sandbox!:           string & !=""
	plateau_iteration?: int & >=0
	prompt?:            string
	note?:              bool
	env?: #StrMap
	mcp_endpoint?:          string
	validate_ai_artifacts?: bool
}
