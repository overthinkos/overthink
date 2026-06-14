// CUE schema for the `init` kind. #Init validates ONE value of the `init:` map
// (InitDef — supervisord/systemd). OPEN tail; *_template fields are Go
// text/template (plain `string`). No #Step (init has no plan).

#Init: {
	candy_field?: [...(string & !="")]
	candy_file?: [...(string & !="")]
	depends_candy?:       string & !=""
	requires_capability?: [...(string & =~"^[a-z][a-z0-9_]*$")]

	// The one mandatory field: the build model.
	model: "fragment_assembly" | "file_copy"

	header_file?:    string & !=""
	fragment_dir?:   string & !=""
	relay_template?: string

	stage_name?:          string & !=""
	stage_header_copy?:   string
	stage_fragment_copy?: string

	assembly_template?:      string
	system_enable_template?: string
	post_assembly_template?: string

	entrypoint?: [...(string & !="")]
	fallback_entrypoint?: [...(string & !="")]

	management_tool?: string & !=""
	management_command?: [string]: string & !=""

	label_key?: string & =~"^ai\\.opencharly\\.service\\.[a-z0-9]+$"

	service_schema?: #InitServiceSchema
	...
}

#InitServiceSchema: {
	service_template?:     string
	unit_path_template?:   string
	dropin_template?:      string
	dropin_path_template?: string
	supports_packaged?:    bool
	...
}
