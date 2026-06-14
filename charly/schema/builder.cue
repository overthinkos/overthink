// CUE schema for the `builder` kind. #Builder validates ONE value of the
// `builder:` map (BuilderDef). OPEN tail; template/script bodies are Go
// text/template (plain `string`). #CacheMount / #PhaseSet / #PhaseTemplates are
// shared (_common.cue). No #Step (builder has no plan).

#Builder: {
	detect_file?: [...(string & !="")]
	detect_config?:    string & !=""
	requires_src_dir?: bool
	inline?:           bool
	cache_mount?: [...#CacheMount]
	env?: [string]:         string
	runtime_env?: [string]: string
	stage_template?:   string
	install_template?: string
	manylinux_fix?:    string
	build_script?:     string & !=""
	phase?:            #PhaseSet
	install_command?: [string]: string
	copy_artifact?: [...#Copy]
	copy_binary?: #Copy
	path_contribution?: [...(string & !="")]
	kind:       *"layer" | "bootstrap"
	privileged: *false | true
	output_artifact?: string & =~"^/"
	if privileged {
		output_artifact!: string & =~"^/"
	}
	...
}

#Copy: {
	src:    string & !=""
	dst:    string & !=""
	chown?: bool
}
