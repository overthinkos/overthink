package main

// Registers the (package-less, shared-scope) egress schemas for the install-ledger
// records charly writes (deploy records + per-candy records).
func init() {
	registerCueKind("deploy_record", "#DeployRecord")
	registerCueKind("candy_record", "#CandyRecord")
}
