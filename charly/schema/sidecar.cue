// CUE schema for the `sidecar` kind. #Sidecar validates ONE sidecar-template
// entity (a value of the `sidecar:` map, e.g. the embedded default library, or
// a per-deploy override). Mirrors Go SidecarDef. CLOSED (an unknown key is a
// typo). Every field optional (struct tags are omitempty; an override supplies a
// subset).

#Sidecar: {
	description?: string & !=""
	image?:       string & !=""
	// env / parameter are map[string]string — values MUST be strings (quote
	// YAML bools/numbers). parameter "" is the "deploy must supply" sentinel.
	env?: [string]:       string
	parameter?: [string]: string
	secret?: [...#SidecarSecret]
	volume?: [...#SidecarVolume]
	security?: #Security
}

#SidecarSecret: {
	name:         string & !=""
	env:          string & !=""
	env_from?:    string // Go text/template, rendered later; CUE checks string only
	description?: string & !=""
}

#SidecarVolume: {
	name: string & !=""
	path: string & =~"^/"
}

// #Security mirrors Go SecurityConfig (container security knobs). Defined here
// for now (only the sidecar kind constrains it); move to _common.cue when a
// second kind needs it (R3).
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

#Size: string & =~"^[0-9]+(\\.[0-9]+)?[kKmMgG]?$"
