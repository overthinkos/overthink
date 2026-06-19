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
_reservedNode: "^(box|candy|bundle|pod|vm|k8s|local|android|host|distro|builder|init|resource|sidecar|agent|group|module|target)$"

// ---------------------------------------------------------------------------
// Per-kind node VALUES — the COMPLETE per-kind def. A node's collections /
// composition / objects live in CHILD nodes, so they are ABSENT from the value
// (each def leaves them optional). The Go parser (node_parse.go) rejects any
// non-scalar field that appears in the value directly — the deterministic
// "everything is a node" gate — while CUE owns the closed-typo / unknown-field /
// wrong-kind-child strictness via the closed def + the child-narrowed arms.
// ---------------------------------------------------------------------------
#BoxValue:      #Box
#CandyValue:    #Candy
#LocalValue:    #Local
#PodValue:      #Pod
#VmValue:       #Vm
#K8sValue:      #K8s
#AndroidValue:  #Android
#DistroValue:   #Distro
#BuilderValue:  #Builder
#InitValue:     #Init
#ResourceValue: #Resource
#SidecarValue:  #Sidecar
#AgentValue:    #Agent
#GroupValue:    #Group
#ModuleValue:   #Module
#TargetValue:   #Target

// A bundle: the deploy config as the value (the COMPLETE #Deploy minus the
// structural nested/peer maps + the derived target — all loader-derived from
// tree position, so authoring any of them is a closed-schema rejection
// (`run: charly migrate`)).
#BundleValue: #Deploy & {nested?: _|_, peer?: _|_, target?: _|_}

// `host:` venue node — names a host (local default implicit); children deploy onto it.
#HostValue: close({
	ssh?:  string & !=""
	user?: string & !=""
})

// ---------------------------------------------------------------------------
// Per-kind ARMS. Each arm pins its one discriminator to a CLOSED per-kind VALUE
// (so a typo'd field / wrong value type in the kind value is a hard error). CHILD
// nodes are accepted structurally (`_`) at this document gate; their strictness is
// LAYERED in the loader: node_parse.go classifies each child and HARD-ERRORS a
// typo'd discriminator ("no discriminator"), a two-discriminator node, and a
// wrong-kind child (a resource/unknown child under a non-deployable kind), and the
// per-entity decode (decodeAndValidateEntityCUE) types every folded data/step child
// against the COMPLETE #Candy/#Deploy/#Step. A pure-CUE per-child kind-disjunction
// here is BOTH an O(entities×kinds×children) blow-up AND ambiguous (a data key like
// `env` also exists on #Step, so #DataChild|#StepChild never resolves) — the layered
// loader checks are exact and fast.
// ---------------------------------------------------------------------------
#BoxArm:     close({box: #BoxValue, {[!~_reservedNode]: _}})
#CandyArm:   close({candy: #CandyValue, {[!~_reservedNode]: _}})
#PodArm:     close({pod: #PodValue, {[!~_reservedNode]: _}})
#VmArm:      close({vm: #VmValue, {[!~_reservedNode]: _}})
#K8sArm:     close({k8s: #K8sValue, {[!~_reservedNode]: _}})
#LocalArm:   close({local: #LocalValue, {[!~_reservedNode]: _}})
#AndroidArm: close({android: #AndroidValue, {[!~_reservedNode]: _}})
#BundleArm:  close({bundle: #BundleValue, {[!~_reservedNode]: _}})
#HostArm:    close({host: #HostValue, {[!~_reservedNode]: _}})
#DistroArm:   close({distro: #DistroValue, {[!~_reservedNode]: _}})
#BuilderArm:  close({builder: #BuilderValue, {[!~_reservedNode]: _}})
#InitArm:     close({init: #InitValue, {[!~_reservedNode]: _}})
#ResourceArm: close({resource: #ResourceValue, {[!~_reservedNode]: _}})
#SidecarArm:  close({sidecar: #SidecarValue, {[!~_reservedNode]: _}})
#AgentArm:    close({agent: #AgentValue, {[!~_reservedNode]: _}})
#GroupArm:    close({group: #GroupValue, {[!~_reservedNode]: _}})
#ModuleArm:   close({module: #ModuleValue, {[!~_reservedNode]: _}})
#TargetArm:   close({target: #TargetValue, {[!~_reservedNode]: _}})

// The unified node — a disjunction of the closed per-kind arms.
#Node: #BoxArm | #CandyArm | #BundleArm | #PodArm | #VmArm | #K8sArm | #LocalArm |
	#AndroidArm | #HostArm | #DistroArm | #BuilderArm | #InitArm | #ResourceArm |
	#SidecarArm | #AgentArm | #GroupArm | #ModuleArm | #TargetArm

// #NodeDoc — a whole charly.yml document in unified node-form: the reserved DOCUMENT
// directives plus a flat map of name-first entity nodes. Validating a document
// against #NodeDoc is the load-time "validate-before-execute" gate.
#NodeDoc: close({
	version?: #CalVer
	repo?:    string & !=""
	import?:  _
	discover?: _
	defaults?: _
	provides?: _
	{[!~"^(version|repo|import|discover|defaults|provides)$"]: #Node}
})
