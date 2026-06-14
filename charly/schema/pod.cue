// CUE schema for the `pod` kind. #Pod validates ONE entry of the `pod:` map
// (PodSpec). Most projects declare no kind:pod entries (the real corpus is all
// `pod: {}`); the schema is modeled from the struct. OPEN tail for the
// deploy-overlay fields a pod entry may carry. Shared #Step from _common.cue.

#Pod: {
	// References a kind:box (bare lowercase-hyphenated name or remote ref).
	box?: =~"^(@.+|[a-z0-9]+(-[a-z0-9]+)*)$"
	sidecar?: [...{...}]
	secret?: [...{...}]
	env_default?: [...(string & =~"^[A-Za-z_][A-Za-z0-9_]*=.*$")]
	plan?: [...#Step]
	...
}
