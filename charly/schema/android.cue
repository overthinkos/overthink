// CUE schema for the `android` kind. #Android validates ONE kind:android entity
// (a device). A device is EITHER an in-pod emulator hosted by a kind:box (box)
// XOR a remote/physical adb endpoint (adb) — never both, never neither. Shared
// #Step from _common.cue.
//
// CLOSED: the base struct (no trailing `...`) models every authored key,
// INCLUDING `box`/`adb` so the closed base permits them — a CLOSED base unified
// with `& ({box:_} | {adb:_})` arms that introduced new keys would reject those
// keys (CUE forbids adding fields outside a closed struct's set). The arms here
// reference only already-allowed `box`/`adb`, tightening them to the
// required-XOR-forbidden (`_|_`) discriminated union without widening the set.
#Android: {
	serial?:         string & !=""
	device?:         string & !=""
	api_level?:      int & >=1
	google_account?: #GoogleAccount
	plan?: [...#Step]

	// Device source (exactly one — the disjunction below enforces XOR).
	box?: string & =~"^[a-z0-9]+(-[a-z0-9]+)*$"
	adb?: #AdbEndpoint
} & ({
	box:  _
	adb?: _|_
} | {
	adb:  _
	box?: _|_
})

// adb endpoint host is "host:port" but MAY carry a ${HOST_PORT:N} var-ref
// (resolved at deploy time), so only non-emptiness is enforced.
#AdbEndpoint: {
	host: string & !=""
}

#GoogleAccount: {
	email_secret?: string & !=""
	token_secret?: string & !=""
}
