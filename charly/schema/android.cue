// CUE schema for the `android` kind. #Android validates ONE kind:android entity
// (a device). A device is EITHER an in-pod emulator hosted by a kind:box (box)
// XOR a remote/physical adb endpoint (adb) — never both, never neither. Shared
// #Step from _common.cue.

#Android: {
	serial?:         string & !=""
	device?:         string & !=""
	api_level?:      int & >=1
	google_account?: #GoogleAccount
	plan?: [...#Step]
	...
} & ({
	box:  =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	adb?: _|_
} | {
	adb:  #AdbEndpoint
	box?: _|_
})

// adb endpoint host is "host:port" but MAY carry a ${HOST_PORT:N} var-ref
// (resolved at deploy time), so only non-emptiness is enforced.
#AdbEndpoint: {
	host: string & !=""
	...
}

#GoogleAccount: {
	email_secret?: string & !=""
	token_secret?: string & !=""
	...
}
