// CUE schema for the `target` kind (Calamares install target / settings.conf).
// #Target validates ONE target entity. No on-disk corpus yet; modeled from
// TargetSpec. Hyphenated Calamares wire keys are quoted. No #Step.

#Target: {
	name:         string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	description?: string & !=""
	"modules-search"?: [...(string & !="")]
	instance?: [...#TargetInstance]
	sequence?: #TargetSequence
	branding?: string & !=""
	"prompt-install": *false | bool
	"dont-chroot":    *false | bool
	"oem-setup":      *false | bool
	"disable-cancel": *false | bool
	group?: [...(string & !="")]
	box?: [...(string & !="")]
	...
}

#TargetInstance: {
	id:      string & !=""
	module:  string & !=""
	config?: string & !=""
	...
}

#TargetSequence: {
	show?: [...(string & !="")]
	exec?: [...(string & !="")]
	...
}
