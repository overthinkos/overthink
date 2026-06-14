package main

// Proves the SCHEMA TIGHTENING has teeth: the closed structs, modeled libvirt/
// cloud_init/deploy subtrees, live-verb method enums, and scalar-coercion parity
// all behave as intended. Each REJECT case would PASS under the old loose
// schema (open `...` / blanket `{...}`) — so this file is the regression guard
// against re-loosening. Each ACCEPT case guards against over-tightening.

import "testing"

// validateKindBody validates a YAML body AS an entity of `kind` (the body is the
// entity's own fields, e.g. a single value of the `vm:`/`deploy:` map — the
// built doc's root value IS the entity).
func validateKindBody(t *testing.T, kind, yamlBody string) error {
	t.Helper()
	doc, err := cueDocFromYAML("t.yml", []byte(yamlBody))
	if err != nil {
		return err
	}
	return validateEntityCUE(kind, "t.yml:"+kind, doc)
}

func TestCueTightening_RejectsAndAccepts(t *testing.T) {
	// candy cases go through the kind-keyed manifest validator.
	candy := func(body string) string {
		return "candy:\n" + body
	}
	const candyHead = "  version: 2026.144.1443\n  name: x\n  description: d\n"
	const candyPlan = "  plan:\n  - check: c\n    file: /x\n"

	cases := []struct {
		name   string
		kind   string // "candy" → manifest validator; else entity-body validator
		yaml   string
		reject bool
	}{
		// --- closed #Candy rejects unknown top-level keys (replaces the Go typo guard) ---
		{"candy unknown top-level key", "candy",
			candy(candyHead + candyPlan + "  requir: [a]\n"), true},
		// --- the autostart/device latent-bug classes the tightening surfaced ---
		{"candy service autostart typo", "candy",
			candy(candyHead + candyPlan + "  service:\n  - name: s\n    exec: /bin/x\n    autostart: true\n"), true},
		{"candy service auto_start correct", "candy",
			candy(candyHead + candyPlan + "  service:\n  - name: s\n    exec: /bin/x\n    auto_start: true\n"), false},
		{"candy security device typo", "candy",
			candy(candyHead + candyPlan + "  security:\n    device: [/dev/fuse]\n"), true},
		{"candy security devices correct", "candy",
			candy(candyHead + candyPlan + "  security:\n    devices: [/dev/fuse]\n"), false},
		// --- scalar-coercion parity: an unquoted int/bool env value is a string ---
		{"candy env scalar int accepted", "candy",
			candy(candyHead + candyPlan + "  env:\n    PORT: 8080\n"), false},
		{"candy env scalar bool accepted", "candy",
			candy(candyHead + candyPlan + "  env:\n    DEBUG: true\n"), false},
		{"candy env PATH forbidden", "candy",
			candy(candyHead + candyPlan + "  env:\n    PATH: /x\n"), true},
		// --- live-verb method enum (cdp): eval is valid, the phantom check is gone ---
		{"candy cdp eval method accepted", "candy",
			candy(candyHead + "  plan:\n  - check: c\n    cdp: eval\n    tab: \"1\"\n    expression: \"1+1\"\n"), false},
		{"candy cdp bogus method rejected", "candy",
			candy(candyHead + "  plan:\n  - check: c\n    cdp: bogus-method\n"), true},
		// --- http status is a plain int, not a matcher list ---
		{"candy http status int accepted", "candy",
			candy(candyHead + "  plan:\n  - check: c\n    http: http://x/\n    status: 200\n    context: [runtime]\n"), false},

		// --- vm: the libvirt blanket {...} is now a CLOSED model ---
		{"vm libvirt unknown field rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  bogus_field: 1\n", true},
		{"vm libvirt modeled field accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nlibvirt:\n  devices:\n    graphics:\n    - type: spice\n", false},
		{"vm cloud_init unknown field rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\ncloud_init:\n  bogus: 1\n", true},
		{"vm cloud_init extra passthrough accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\ncloud_init:\n  extra: |\n    #cloud-config\n", false},
		// --- vm source discriminated union: cross-arm field forbidden ---
		{"vm cloud_image with bootc field rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\n  box: y\n", true},

		// --- deploy: the sidecar/probes blankets are modeled; unknown key rejected ---
		{"deploy unknown key rejected", "deploy",
			"target: pod\nbox: x\nbogus_key: 1\n", true},
		{"deploy modeled fields accepted", "deploy",
			"target: pod\nbox: x\nrestart: always\nport: [\"8080:8080\"]\n", false},
		{"deploy bad restart enum rejected", "deploy",
			"target: pod\nbox: x\nrestart: sometimes\n", true},

		// --- box: closed; the retired port: field is forbidden ---
		{"box unknown key rejected", "box",
			"name: x\nbogus: 1\n", true},
		{"box retired port field rejected", "box",
			"name: x\nport: [\"8080:8080\"]\n", true},

		// --- candy apk: exactly-one package/apk + closed (the matchN→disjunction fix) ---
		{"candy apk package only accepted", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - package: com.x\n"), false},
		{"candy apk both package and apk rejected", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - package: com.x\n    apk: /tmp/x.apk\n"), true},
		{"candy apk neither rejected", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - arch: x86_64\n"), true},
		{"candy apk unknown field rejected", "candy",
			candy(candyHead + candyPlan + "  apk:\n  - package: com.x\n    bogus: 1\n"), true},

		// --- vm ssh: port⊕port_auto mutex + closed (the matchN→disjunction fix) ---
		{"vm ssh port only accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nssh:\n  port: 2222\n", false},
		{"vm ssh port_auto only accepted", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nssh:\n  port_auto: true\n", false},
		{"vm ssh port + port_auto rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nssh:\n  port: 2222\n  port_auto: true\n", true},
		{"vm ssh unknown field rejected", "vm",
			"source:\n  kind: cloud_image\n  url: http://x\nssh:\n  bogus: 1\n", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.kind == "candy" {
				err = validateCandyManifestCUE("t.yml", []byte(tc.yaml))
			} else {
				err = validateKindBody(t, tc.kind, tc.yaml)
			}
			if tc.reject && err == nil {
				t.Errorf("expected CUE to REJECT %q, but it passed", tc.name)
			}
			if !tc.reject && err != nil {
				t.Errorf("expected CUE to ACCEPT %q, but it failed: %v", tc.name, err)
			}
		})
	}
}
