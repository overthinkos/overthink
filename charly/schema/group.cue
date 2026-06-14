// CUE schema for the `group` kind (Calamares-style netinstall package group).
// #Group validates ONE group entity. No on-disk corpus yet; modeled from
// GroupSpec. RECURSIVE via subgroup. No #Step.

#Group: {
	name:         string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
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
	...
}

// bare scalar shorthand XOR object form.
#PackageItem: (string & !="") | {
	name:         string & !=""
	description?: string & !=""
	...
}

#DistroPackages: {
	package?: [...#PackageItem]
	copr?: [...(string & !="")]
	repo?: [...{...}]
	exclude?: [...(string & !="")]
	option?: [...(string & !="")]
	module?: [...(string & !="")]
	aur?: #AUR
	...
}

#AUR: {
	package?: [...#PackageItem]
	option?: [...(string & !="")]
	replace?: [...(string & !="")]
	...
}
