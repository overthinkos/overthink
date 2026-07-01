// CUE schema for the UNIFIED `#Node` model — ONE name-first node shape for ALL of
// charly.yml: `<name>: {<kind>: <SCALARS>, <child-node>…}`.
//
// EVERYTHING IS A NODE. A kind's discriminator value holds only the entity's SCALAR
// fields; every NON-scalar field (composition, collection, single object) is a CHILD
// node `<name>-<key>: {<key>: <value>}`, every plan step is a CHILD step node, and a
// resource nested under a deployable kind is a sub-ENTITY child. The assembler
// (node_build.go) folds the data + step children back into the entity body and
// decodes it through the COMPLETE per-kind def (#Candy/#Deploy/…), so the strict
// field typing comes from the complete def — there are no per-arm value gaps.
//
// STRICTNESS: #Node is a DISJUNCTION of per-kind CLOSED arms (never matchN — that
// disables closedness). Each arm pins its one discriminator to a SCALAR-only kind
// value (#ForbidCollections forbids every non-scalar field in the value, forcing it
// to be a child) and narrows children to the legal set: data/step children
// (#ChildCommon) everywhere, plus resource arms (#ChildResource) under a deployable
// kind. A typo'd discriminator, an unknown value field, a wrong-kind child, and two
// discriminators all FAIL closedness.

// Reserved discriminators = the CORE kind keywords (the kinds with a #Node arm).
// Negated regex so a child's NAME cannot shadow a kind keyword. The plugin-extracted
// kinds (agent/module/sidecar/package-group + distro/builder/init/resource/target,
// group — C2-group, AND the 5 substrate kinds pod/vm/k8s/local/android — C2-substrate)
// are NOT here — they have no arm and are recognized dynamically by the loader via a
// registered ClassKind provider (classifyDisc → providerRegistry.ResolveKind). group +
// the substrates keep their #ResourceKind membership (so the loader still nests their
// members) while their VALUE is validated HOST-SIDE against the KEPT core value defs
// (#PodValue/#VmValue/… below) in runPluginKind — a self-contained plugin schema cannot
// carry the rich core-typed substrate value (it references #Deploy/#Vm/#LibvirtDomain/…),
// unlike group's small self-contained #GroupInput — and their authored members / template
// ride op.Env (F5 authored-member INPUT-threading + its substrate-TEMPLATE fold arm).
// Only `candy` retains a #Node arm (its box⊻layer value is core-typed and stays in-proc).
_reservedNode: "^(candy)$"

// #ResourceKind — the DEPLOYABLE kinds: the kinds that nest a sub-ENTITY (resource)
// child (a deploy-into / alongside member), so a `<name>: {<kind>:…, <child>:
// {<resource-kind>:…}}` node is a bundle-shaped node. Every OTHER kind admits only
// data + step children. The loader (node_parse.go) classifies a resource child against
// this set; schemagen emits it as spec.ResourceKinds so the Go side derives the
// deployable vocabulary from this ONE CUE source instead of a hand list. (node.cue's
// per-arm child gate stays structural `_` — the deployable-vs-not check is the layered
// loader check; this enum is its single vocabulary source.)
//
// NOTE: `group` is a member of #ResourceKind but has NO #Node arm (C2-group externalized
// it to candy/plugin-group). So #ResourceKind ⊄ the arm-derived KindWords — the two enums
// are independent: KindWords = the CORE kinds with a #Node arm (arm-validated values);
// #ResourceKind = the kinds that nest members (arm-validated OR plugin-served). group's
// members are pre-decoded host-side (buildResourceMemberChildren) and threaded to
// plugin-group via op.Env (F5); the parser gate admits them because resourceKindSet has group.
#ResourceKind: ("pod" | "vm" | "k8s" | "local" | "android" | "group") @go(-)

// ---------------------------------------------------------------------------
// Per-kind node VALUES — the COMPLETE per-kind def. A node's collections /
// composition / objects live in CHILD nodes, so they are ABSENT from the value
// (each def leaves them optional). The Go parser (node_parse.go) rejects any
// non-scalar field that appears in the value directly — the deterministic
// "everything is a node" gate — while CUE owns the closed-typo / unknown-field /
// wrong-kind-child strictness via the closed def + the child-narrowed arms.
// ---------------------------------------------------------------------------
// EDGE-INHERIT cutover D: `box:` merges INTO `candy:`. A `candy:` node is EITHER a
// LAYER fragment (#Candy, no base/from) OR a full IMAGE (#Image — a #Box that REQUIRES
// `base:` or `from:`, the former box:), routed by that marker in the loader
// (candyKind.DecodeNode → uf.Box vs uf.Candy). The image arm REQUIRES base⊻from, and
// #Candy is the DEFAULT (`*`): an IMAGE carries base/from → bottoms #Candy (closed, no
// base) → resolves to #Image; a LAYER carries neither → #Image is incomplete (its base/
// from required-but-absent) while #Candy matches → the default resolves it to #Candy.
// That keeps the disjunction CONCRETELY validatable (a raw `#Candy | #Box` is NOT: a
// required-but-absent field is INCOMPLETE, not bottom, so a layer never eliminates the
// image arm). @go(-): the Go types are spec.Candy + spec.Box (decode picks by shape).
#Image:      #Box & ({base: string & !=""} | {from: string & !=""})
#CandyValue: (*#Candy | #Image) @go(-)
// EDGE-INHERIT cutover B: a substrate kind is BOTH the template entity AND the deploy
// (the eliminated `bundle:` role folds in). The value is the disjunction
// `#Template | #Deploy`, routed by SHAPE in the loader (a template carries its own
// config — source:/composition; a deploy carries from:/image: + the deploy config).
// The RDD spike proved `cue vet` resolves this disjunction unambiguously even with
// overlapping fields. @go(-): the Go types come from #Local/#Pod/#Vm/#K8s/#Android +
// #Deploy directly; this value def is validation-only.
//
// C2-substrate: these 5 substrate kinds have NO #Node arm anymore (externalized to
// candy/plugin-substrate, mirroring group). They are KEPT here as the HOST-SIDE value
// gate: runPluginKind validates a substrate node's authored value against #<Kind>Value
// (validateStandaloneKindValueCUE) — the SAME closedness the #Node arm gave — because a
// self-contained plugin schema cannot carry these rich core-referencing values. So these
// defs stay REACHABLE from Go (cue_kind_*.go registerCueKind maps the kind → its value
// def) while contributing NO #Node arm (KindWords drops the 5).
#LocalValue:   (#Local | #DeployValue) @go(-)
#PodValue:     (#Pod | #DeployValue) @go(-)
#VmValue:      (#Vm | #DeployValue) @go(-)
#K8sValue:     (#K8s | #DeployValue) @go(-)
#AndroidValue: (#Android | #DeployValue) @go(-)
// The build-vocabulary kinds (`distro:`/`builder:`/`init:`/`resource:`), the Calamares
// install `target:`, the Calamares package group (`package-group:`), the AI-CLI grader
// catalog (`agent:`), the Calamares installer module (`module:`), the sidecar-template
// library (`sidecar:`), the targetless deploy group (`group:` — C2-group), AND the 5
// substrate kinds (`pod:`/`vm:`/`k8s:`/`local:`/`android:` — C2-substrate) are no longer
// core kinds — each was extracted into a dedicated plugin unit, so none has a #Node arm;
// such a node passes #NodeDoc as a registered non-core discriminator. A plugin with a
// self-contained served #*Input schema (distro/builder/…/group) is validated by that
// schema (runPluginKind → validateAuthoredPluginInput); the 5 substrates, whose value is
// rich + core-referencing (#Vm/#Deploy/#LibvirtDomain/…) and so cannot be a self-contained
// plugin schema, are validated HOST-SIDE against the KEPT #<Kind>Value defs above
// (runPluginKind → validateStandaloneKindValueCUE). The core #Distro / #Builder / #Init /
// #Resource / #Target / #Agent / #Module / #Sidecar / #Pod / #Vm / #K8s / #Local /
// #Android / #Deploy defs (schema/*.cue) are KEPT — they still generate spec.Distro /
// spec.Vm / … (the canonical types the plugins' Invoke and the host decode into). For
// `group` the plugin (candy/plugin-group) decodes its scalar VALUE into the core
// spec.Deploy (#Deploy, kept via cue_kind_deploy.go) and attaches the host-threaded
// authored members; for the substrates candy/plugin-substrate ECHOES the host-pre-decoded
// canonical node (deploy BundleNode or per-substrate template) the host folds into
// uf.Bundle / uf.Pod / uf.VM / … (C2-substrate template fold arm).

// #DeployValue — the AUTHORED deploy shape (the disjunct under each substrate arm):
// the COMPLETE #Deploy minus the structural nested/peer maps + the derived target —
// all loader-derived from tree position, so authoring any of them is a closed-schema
// rejection (`run: charly migrate`). The substrate kind at the EDGE supplies the
// target; from:/image: supply the cross-ref (EDGE-INHERIT cutover B; was #BundleValue).
#DeployValue: #Deploy & {nested?: _|_, peer?: _|_, target?: _|_, member_of?: _|_, inside?: _|_}

// ---------------------------------------------------------------------------
// Per-kind ARMS. Each arm pins its one discriminator to a CLOSED per-kind VALUE
// (so a typo'd field / wrong value type in the kind value is a hard error). CHILD
// nodes are accepted structurally (`_`) at this document gate; their strictness is
// LAYERED: node_parse.go classifies each child and HARD-ERRORS a typo'd
// discriminator ("no discriminator"), a two-discriminator node, and a wrong-kind
// child (a resource/unknown child under a non-deployable kind). The step-CHILD Op
// fields are typed against the closed #Step/#Op by the VALIDATE entrypoint (charly
// box validate → validateNodeFormSteps, cue_schema.go) — NOT at this document gate
// and NOT at decode (decodeEntityViaCUE decodes leniently). A pure-CUE per-child
// kind-disjunction here is BOTH an O(entities×kinds×children) blow-up AND ambiguous
// (a data key like `env` also exists on #Step, so #DataChild|#StepChild never
// resolves) — the layered loader + validate checks are exact and fast.
// ---------------------------------------------------------------------------
#CandyArm: close({candy: #CandyValue, {[!~_reservedNode]: _}})

// The unified node — the ONE remaining closed per-kind arm (`candy`, the box⊻layer
// factory whose value is core-typed and decoded in-proc). `group` (C2-group) and the 5
// substrate kinds pod/vm/k8s/local/android (C2-substrate) have NO arm — they were
// externalized to candy/plugin-group / candy/plugin-substrate: such a node passes #NodeDoc
// via #CandyArm's open `{[!~_reservedNode]: _}` pattern (none of those words is in
// _reservedNode) and its VALUE is validated HOST-SIDE — group by candy/plugin-group's
// served #GroupInput, the substrates against the KEPT #<Kind>Value defs above
// (runPluginKind → validateStandaloneKindValueCUE). #Node stays a (single-arm) disjunction
// so nodeDiscriminators derives KindWords = {candy} — the externalized kinds resolve via
// their registered ClassKind provider (recognizedKind), not a #Node arm.
#Node: #CandyArm

// #NodeDoc — a whole charly.yml document in unified node-form: the reserved DOCUMENT
// directives plus a flat map of name-first entity nodes. Validating a document
// against #NodeDoc is the load-time "validate-before-execute" gate.
#NodeDoc: close({
	version?:  #CalVer
	repo?:     string & !=""
	import?:   _
	discover?: _
	defaults?: _
	provides?: _
	// providers — the registry membership manifest (provider class → the words it
	// contributes). Read at init by registry_bootstrap.go to drive built-in provider
	// registration from the embedded charly.yml (the binary's default config);
	// recognized here so a document carrying it is not mis-read as a node named
	// "providers". A gate keeps it in bijection with the compiled-in instances.
	providers?: {[string]: [...string]}
	// compiled_plugins — the plugin CANDIES compiled into the charly binary (in-proc
	// placement). Read by the pluginsgen generator to emit charly/plugins_generated.go
	// (registerCompiledPlugin per entry) + the repo-root go.work; recognized here so a
	// document carrying it is not mis-read as a node named "compiled_plugins". A plugin
	// candy not listed still loads OUT-OF-PROCESS when referenced (the coexist path).
	compiled_plugins?: [...string]
	// context_ignore_baseline — the built-in build-context ignore patterns (VCS/binary
	// excludes + cache-hygiene globs), formerly the Go var baselineContextIgnore, now
	// data in the embedded charly.yml. Read by generate.go to emit .containerignore /
	// .dockerignore; a project's defaults.context_ignore still overlays on top.
	context_ignore_baseline?: [...string]
	// install_hints — binary name → (distro ID → package name) for `charly doctor` host
	// dependency install suggestions, formerly the Go var installHints (distro.go), now
	// data in the embedded charly.yml. An "AUR: <cmd>" value carries its own install line.
	install_hints?: {[string]: {[string]: string}}
	// ovmf_paths — distro family (fedora|arch|debian) → secure/nonsecure ordered OVMF
	// firmware candidate path pairs, formerly inline literals in ovmf_paths.go. The
	// alias→family resolution + secure selection + unknown-distro union stay Go logic;
	// only the path DATA is here. {code, vars} per candidate.
	ovmf_paths?: {[string]: {secure: [...{code: string, vars: string}], nonsecure: [...{code: string, vars: string}]}}
	// device_descriptions — host device path → human description for `charly doctor`'s
	// hardware section, formerly the Go var deviceDescriptions (doctor.go); data, not code.
	device_descriptions?: {[string]: string}
	// device_patterns — host device glob patterns probed for auto-detection
	// (DetectHostDevices) + `charly doctor`'s hardware section, formerly the Go var
	// devicePatterns (devices.go); data, not code.
	device_patterns?: [...string]
	// gpu_vendors — PCI vendor ID → name for the render nodes that count as a real,
	// encode-capable GPU (vs the paravirtual virtio-gpu), formerly the inline switch in
	// pickRenderNode (devices.go); key membership picks the DRINODE render node.
	gpu_vendors?: {[string]: string}
	// pci_class_labels — PCI class code (high 16 bits) → human label for VFIO passthrough
	// device reporting, formerly the inline switch in pciClassLabel (devices.go); an
	// unknown class falls back to the raw class (logic, not data).
	pci_class_labels?: {[string]: string}
	// distro_package_managers — host distro ID → install command prefix for `charly
	// doctor` install hints, formerly the inline switch in parseOsRelease (distro.go).
	distro_package_managers?: {[string]: string}
	// distro_family_map — host distro ID → base family for install-hint package-name
	// lookup, formerly the inline switch in distroFamily (distro.go); an unlisted distro
	// maps to itself (logic, not data).
	distro_family_map?: {[string]: string}
	// ovmf_distro_aliases — host distro ID → OVMF firmware family (fedora|arch|debian),
	// formerly the inline switches in ovmfCandidatesForDistro + ovmfNotFoundError
	// (ovmf_paths.go); an unlisted distro tries the union of all families (logic).
	ovmf_distro_aliases?: {[string]: string}
	{[!~"^(version|repo|import|discover|defaults|provides|providers|compiled_plugins|context_ignore_baseline|install_hints|ovmf_paths|device_descriptions|device_patterns|gpu_vendors|pci_class_labels|distro_package_managers|distro_family_map|ovmf_distro_aliases)$"]: #Node}
})
