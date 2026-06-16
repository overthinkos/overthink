package main

import "testing"

// Regression test for the no-candy-deploy empty-deploy_id egress bug, surfaced by
// measuring the boot-only VM check bed check-arch-pacstrap-vm (no add_candy: — it
// only proves the guest boots). With no candy plans, firstDeployID returns "",
// the deploy record's deploy_id is empty, and the egress #DeployRecord schema
// (deploy_id != "") hard-fails the write (the ledger filename also collapses to
// ".json"). resolveDeployID must therefore NEVER return empty.
func TestResolveDeployID_NeverEmpty(t *testing.T) {
	// No candy plans (boot-only VM deploy) → stable hashed fallback from the target.
	got := resolveDeployID(nil, "vm:arch-pacstrap")
	if got == "" {
		t.Fatal(`resolveDeployID returned "" for a no-candy deploy; egress #DeployRecord requires deploy_id != ""`)
	}
	if len(got) != 16 {
		t.Errorf("fallback deploy id = %q (len %d), want a 16-hex computeDeployID hash", got, len(got))
	}
	// Deterministic: same target → same fallback id.
	if a, b := resolveDeployID(nil, "vm:x"), resolveDeployID(nil, "vm:x"); a != b {
		t.Error("fallback deploy id must be deterministic for the same target")
	}
	// Distinct targets → distinct ids.
	if resolveDeployID(nil, "vm:x") == resolveDeployID(nil, "vm:y") {
		t.Error("distinct targets should yield distinct fallback ids")
	}
	// A real plan id always wins over the fallback.
	plans := []*InstallPlan{{DeployID: "abc1230000000000"}}
	if id := resolveDeployID(plans, "vm:ignored"); id != "abc1230000000000" {
		t.Errorf("resolveDeployID = %q, want the plan's id", id)
	}
}

// End-to-end: the fallback id makes a no-candy deploy record satisfy the egress
// #DeployRecord schema, while an empty id is correctly rejected (proving the
// validation has teeth and the fix is load-bearing).
func TestResolveDeployID_FallbackPassesEgress(t *testing.T) {
	rec := &DeployRecord{
		DeployID:   resolveDeployID(nil, "vm:arch-pacstrap"),
		Target:     "vm:arch-pacstrap",
		DeployedAt: "2026-06-16T00:00:00Z",
	}
	if err := ValidateEgressValue("deploy_record", "no-candy-vm", rec); err != nil {
		t.Fatalf("no-candy deploy record should pass egress validation, got: %v", err)
	}
	bad := &DeployRecord{DeployID: "", Target: "vm:x", DeployedAt: "2026-06-16T00:00:00Z"}
	if err := ValidateEgressValue("deploy_record", "empty-id", bad); err == nil {
		t.Error("empty deploy_id must FAIL egress validation, but it passed")
	}
}
