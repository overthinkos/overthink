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

// Reserved discriminators = the kind keywords. Negated regex so a child's NAME
// cannot shadow a kind keyword.
_reservedNode: "^(candy|pod|vm|k8s|local|android|distro|builder|init|resource|sidecar|agent|group|package-group|module|target)$"

// #ResourceKind — the DEPLOYABLE subset of the kind keywords: the kinds whose
// #Node arm admits a sub-ENTITY (resource) child (a deploy-into / alongside
// member), so a `<name>: {<kind>:…, <child>: {<resource-kind>:…}}` node is a
// bundle-shaped node. Every OTHER kind admits only data + step children. The
// loader (node_parse.go) classifies a resource child against this set; schemagen
// emits it as spec.ResourceKinds so the Go side derives the deployable vocabulary
// from this ONE CUE source instead of a hand list. (node.cue's per-arm child gate
// stays structural `_` — the deployable-vs-not check is the layered loader check;
// this enum is its single vocabulary source.)
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
// (the eliminated `bundle:` role folds in). One arm accepts the disjunction
// `#Template | #Deploy`, routed by SHAPE in the loader (a template carries its own
// config — source:/composition; a deploy carries from:/image: + the deploy config).
// The RDD spike proved `cue vet` resolves this disjunction unambiguously even with
// overlapping fields. @go(-): the Go types come from #Local/#Pod/#Vm/#K8s/#Android +
// #Deploy directly; this value def is validation-only (the arm is the load gate).
#LocalValue:   (#Local | #DeployValue) @go(-)
#PodValue:     (#Pod | #DeployValue) @go(-)
#VmValue:      (#Vm | #DeployValue) @go(-)
#K8sValue:     (#K8s | #DeployValue) @go(-)
#AndroidValue: (#Android | #DeployValue) @go(-)
#DistroValue:  #Distro
#BuilderValue:  #Builder
#InitValue:     #Init
#ResourceValue: #Resource
#SidecarValue:  #Sidecar
#AgentValue:    #Agent
// group: is a TARGETLESS deploy group (#Deploy with members, no own workload — the
// former targetless `bundle:`). EDGE-INHERIT cutover C moved the Calamares package
// group to its own `package-group:` kind, so `group:` is now UNAMBIGUOUSLY the deploy
// group (no shape routing). @go(-): the Go type is BundleNode via the loader.
#GroupValue:        #DeployValue @go(-)
#PackageGroupValue: #Group
#ModuleValue:       #Module
#TargetValue:       #Target

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
#PodArm: close({pod: #PodValue, {[!~_reservedNode]: _}})
#VmArm: close({vm: #VmValue, {[!~_reservedNode]: _}})
#K8sArm: close({k8s: #K8sValue, {[!~_reservedNode]: _}})
#LocalArm: close({local: #LocalValue, {[!~_reservedNode]: _}})
#AndroidArm: close({android: #AndroidValue, {[!~_reservedNode]: _}})
#DistroArm: close({distro: #DistroValue, {[!~_reservedNode]: _}})
#BuilderArm: close({builder: #BuilderValue, {[!~_reservedNode]: _}})
#InitArm: close({init: #InitValue, {[!~_reservedNode]: _}})
#ResourceArm: close({resource: #ResourceValue, {[!~_reservedNode]: _}})
#SidecarArm: close({sidecar: #SidecarValue, {[!~_reservedNode]: _}})
#AgentArm: close({agent: #AgentValue, {[!~_reservedNode]: _}})
#GroupArm: close({group: #GroupValue, {[!~_reservedNode]: _}})
#PackageGroupArm: close({"package-group": #PackageGroupValue, {[!~_reservedNode]: _}})
#ModuleArm: close({module: #ModuleValue, {[!~_reservedNode]: _}})
#TargetArm: close({target: #TargetValue, {[!~_reservedNode]: _}})

// The unified node — a disjunction of the closed per-kind arms.
#Node: #CandyArm | #PodArm | #VmArm | #K8sArm | #LocalArm |
	#AndroidArm | #DistroArm | #BuilderArm | #InitArm | #ResourceArm |
	#SidecarArm | #AgentArm | #GroupArm | #PackageGroupArm | #ModuleArm | #TargetArm

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
	{[!~"^(version|repo|import|discover|defaults|provides)$"]: #Node}
})
