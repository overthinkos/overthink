// CUE schema for the `agent` kind. #Agent validates ONE value of the `agent:`
// map (AgentConfig — the AI-CLI grader catalog). CLOSED (AgentConfig is fully
// enumerated; an unknown key is a typo). No #Step (an agent has no plan).

#Agent: {
	description?: string & !=""
	command: [string, ...string]            // >=1, all strings
	prompt_via: *"argv" | "file"
	version_command?: [...string]
	timeout?: #Duration
	env?: #StrMap
	working_dir?: string & !=""
	credential?: [...#CredentialMount]
	progress_check_interval?:         #Duration
	progress_no_improvement_timeout?: #Duration
	output_format: *"" | "stream-json"
}

#CredentialMount: {
	src:       string & !=""
	dst:       string & !=""
	mode?:     "copy" | "bind"
	optional?: bool
}

// #Duration now lives in _common.cue (shared by agent + deploy + #Op).
