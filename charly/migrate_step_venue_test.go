package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// node-form fixture: a group iterate bed with flat pod: steps (bare + dotted) and
// a workload cross bed with on:-driver steps + PEER_* address vars — the exact
// pre-cutover shapes MigrateStepVenue rewrites.
const stepVenueFixture = `version: 2026.169.0002
default:
    bundle:
        description: bench
    default-step-0:
        check: the os marker is present
        file: /etc/charly-os-marker
        pod: os
    default-step-1:
        check: redis answers
        command: redis-cli ping
        pod: redis
    default-step-1b:
        run: seed a key (venue-less provisioning — inherits the redis phase)
        command: redis-cli set k v
    default-step-2:
        check: nested redis answers
        command: redis-cli ping
        pod: nested-check-vm.inner-app-pod.nested-redis-pod
cross:
    bundle:
        box: web
        disposable: true
    chrome:
        bundle:
            box: chrome-headless
    cross-fixture-up:
        check: the subject serves its marker
        http: http://127.0.0.1:8080/
        status: 200
    cross-step-0:
        check: chrome renders the subject
        cdp: text
        on: chrome
        stdout:
            contains: ["${PEER_HOST:cross}"]
    cross-step-1:
        check: the host reaches the subject
        command: "curl ${PEER_ENDPOINT:cross:8080}"
        on: chrome
`

func parseDoc(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &n
}

func marshalDoc(t *testing.T, n *yaml.Node) string {
	t.Helper()
	b, err := yaml.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestMigrateStepVenue_FlatToTree(t *testing.T) {
	doc := parseDoc(t, stepVenueFixture)
	if !stepVenueDoc(doc) {
		t.Fatalf("stepVenueDoc reported no change on a config with pod:/on: steps")
	}
	out := marshalDoc(t, doc)

	// No retired venue OVERRIDES survive on steps. (`pod:` still appears as the
	// resource-node disc, so assert on the authored override spellings instead.)
	if strings.Contains(out, "pod: os") || strings.Contains(out, "pod: redis") ||
		strings.Contains(out, "pod: nested-check-vm") {
		t.Errorf("a step still carries a pod: venue override:\n%s", out)
	}
	if strings.Contains(out, "on: chrome") {
		t.Errorf("a step still carries an on: driver override:\n%s", out)
	}
	// PEER_* rewritten to the unified ${HOST:…}; :port preserved.
	if strings.Contains(out, "${PEER_HOST:") || strings.Contains(out, "${PEER_ENDPOINT:") {
		t.Errorf("PEER_* var not rewritten:\n%s", out)
	}
	if !strings.Contains(out, "${HOST:cross}") || !strings.Contains(out, "${HOST:cross:8080}") {
		t.Errorf("expected ${HOST:cross} and ${HOST:cross:8080} after rewrite:\n%s", out)
	}
	// Agent-provisioned scaffolds synthesized for the pod: venues; the existing
	// chrome peer keeps its box and is NOT made agent-provisioned.
	if !strings.Contains(out, "agent_provisioned: true") {
		t.Errorf("expected agent-provisioned member scaffolds:\n%s", out)
	}
	if !strings.Contains(out, "box: chrome-headless") {
		t.Errorf("existing chrome peer lost its box:\n%s", out)
	}

	// step-venue runs BEFORE edge-inherit in the chain; apply edge-inherit in-memory
	// so the intermediate bundle:-form deploys become loadable substrate-kind nodes
	// (EDGE-INHERIT cutover B).
	edgeInheritDoc(doc)

	// Structural: the migrated tree parses node-form AND passes flattenBundleVenues
	// (no "group has direct steps"), with venues stamped from position.
	root := rootMappingNode(doc)
	uf := &UnifiedFile{Bundle: map[string]BundleNode{}}
	for _, name := range []string{"default", "cross"} {
		entityVal := findMappingValue(root, name)
		if entityVal == nil {
			t.Fatalf("entity %q missing after migration", name)
		}
		gn, err := parseNode(name, entityVal, false)
		if err != nil {
			t.Fatalf("parseNode(%q): %v", name, err)
		}
		dn, err := buildBundleNode(gn)
		if err != nil {
			t.Fatalf("buildBundleNode(%q): %v", name, err)
		}
		uf.Bundle[name] = *dn
	}
	if err := flattenBundleVenues(uf); err != nil {
		t.Fatalf("flattenBundleVenues on migrated tree: %v", err)
	}
	gotVenues := map[string]bool{}
	for _, name := range []string{"default", "cross"} {
		for _, s := range uf.Bundle[name].Plan {
			gotVenues[s.Venue] = true
		}
	}
	// `chrome` is a child of the WORKLOAD root `cross` (box: web), so its venue is
	// the dotted `cross.chrome` (nested), NOT a bare peer — faithful to tree
	// position. The no-on: subject step `cross-fixture-up` runs on the workload
	// root → venue `cross`. The iterate-bed agent-provisioned members are bare.
	for _, want := range []string{
		"os", "redis", "nested-check-vm.inner-app-pod.nested-redis-pod", "cross.chrome", "cross",
	} {
		if !gotVenues[want] {
			t.Errorf("missing position-derived venue %q after flatten; got %v", want, gotVenues)
		}
	}
}

func TestMigrateStepVenue_Idempotent(t *testing.T) {
	doc := parseDoc(t, stepVenueFixture)
	if !stepVenueDoc(doc) {
		t.Fatalf("first run reported no change")
	}
	first := marshalDoc(t, doc)

	// Second run: no change, byte-identical.
	if stepVenueDoc(doc) {
		t.Errorf("second run reported a change — not idempotent")
	}
	second := marshalDoc(t, doc)
	if first != second {
		t.Errorf("re-run not byte-identical:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	// A freshly-parsed already-migrated doc is also a no-op (the persisted form
	// is stable across a parse→migrate cycle).
	doc2 := parseDoc(t, first)
	if stepVenueDoc(doc2) {
		t.Errorf("migrating an already-migrated doc reported a change")
	}
}

// TestMigrateStepVenue_ReservedVenueErrors verifies the R3-generic defense: a
// step whose `pod:` venue is a reserved kind keyword (it cannot become a tree
// member node) makes the migrator HARD-ERROR with a rename hint, rather than
// silently emit an invalid member the loader later rejects with a cryptic CUE
// "field not allowed" error.
func TestMigrateStepVenue_ReservedVenueErrors(t *testing.T) {
	dir := t.TempDir()
	doc := `version: 2026.169.0002
mybed:
    bundle:
        disposable: true
    mybed-step-0:
        check: at least one cluster node is listable
        k8s: nodes
        pod: k8s
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := MigrateStepVenue(dir, true)
	if err == nil {
		t.Fatalf("expected a hard error for the reserved-keyword venue %q, got nil", "k8s")
	}
	if !strings.Contains(err.Error(), "reserved") || !strings.Contains(err.Error(), "k8s-cluster") {
		t.Errorf("error must name the reserved collision + a rename hint (e.g. k8s-cluster); got: %v", err)
	}
}
