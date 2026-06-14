// CUE schema for the `distro` kind. #Distro validates ONE value of the `distro:`
// map (DistroDef) — the build vocabulary. OPEN tail; the real invariants (URLs,
// version/name patterns, required base_user/local_pkg fields) are constrained.
// TEXT/TEMPLATE fields are Go text/template — plain `string`, never parsed.
// #CacheMount / #PhaseSet / #PhaseTemplates are shared (_common.cue).

#Distro: {
	inherits?:         string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	inherit_packages?: bool
	version?:          string & =~"^[0-9]+(\\.[0-9]+)*$"
	bootstrap?:        #Bootstrap
	workaround?: [...string]
	format?: {[string]: #Format}
	base_user?:        #BaseUser
	pacstrap?:         #Pacstrap
	debootstrap?:      #Debootstrap
	alpine_bootstrap?: #AlpineBootstrap
	bootloader?:       #Bootloader
	dnf?:              #Dnf
	...
}

#Bootstrap: {
	install_cmd: string
	package?: [...string]
	cache_mount?: [...#CacheMount]
	...
}

#Format: {
	cache_mount?: [...#CacheMount]
	section_field?: {[string]: "list" | "list_of_maps"}
	install_template?:   string
	uninstall_template?: string
	phase?:              #PhaseSet
	validate?: [...#FormatRule]
	secondary?: bool
	local_pkg?: #LocalPkg
	...
}

#FormatRule: {
	field: string & !=""
	rule:  string & !=""
	...
}

#LocalPkg: {
	pkg_glob:           string & !=""
	source_sentinel:    string & !=""
	build_template:     string & !=""
	install_template:   string & !=""
	probe:              string & !=""
	dep_builder?:       string
	download_template?: string
	...
}

#Pacstrap: {
	base_package?: [...string]
	keyring_init_cmd?: string
	mirrorlist_url?:   string & =~"^https?://"
	extra_repo?: [...#PacstrapRepo]
	runtime_pacman_conf?: string
	...
}
#PacstrapRepo: {
	name:      string & !=""
	server:    string & =~"^https?://"
	siglevel?: string
	...
}
#Debootstrap: {
	suite?:   string
	mirror?:  string & =~"^https?://"
	variant?: string
	components?: string
	include_package?: [...string]
	base_package?: [...string]
	extra_repo?: [...#DebootstrapRepo]
	...
}
#DebootstrapRepo: {
	name:        string & !=""
	url:         string & =~"^https?://"
	suite?:      string
	components?: string
	...
}
#AlpineBootstrap: {
	mirror_url?: string & =~"^https?://"
	...
}
#Bootloader: {
	install_template?:   string
	initramfs_template?: string
	fstab_template?:     string
	...
}
#BaseUser: {
	name: string & !=""
	uid:  int & >=0
	gid:  int & >=0
	home: string & =~"^/"
	...
}
#Dnf: {
	max_parallel_downloads?: int & >=1
	fastestmirror?:          bool
	...
}
