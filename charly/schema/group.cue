// CUE schema for the `group` kind (Calamares-style netinstall package group).
// #Group validates ONE group entity. No on-disk corpus yet; modeled from
// GroupSpec. RECURSIVE via subgroup. CLOSED. #PackageItem / #DistroPackages /
// #AUR / #RepoBlock are shared (_common.cue). No #Step.

#Group: {
	name:         #EntityRef
	description?: string & !=""
	package?: [...#PackageItem]
	distro?: {[string]: #DistroPackages}
	hidden:       *false | bool
	selected:     *false | bool
	critical:     *false | bool
	immutable:    *false | bool
	expanded:     *false | bool
	noncheckable: *false | bool
	pre_install?:  string & !=""
	post_install?: string & !=""
	source?:       string & !=""
	subgroup?: [...#Group]
	require?: [...(string & !="")]
}
