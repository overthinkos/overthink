export const meta = {
  name: 'verify-status',
  description:
    'Substrate-coverage fan-out for the unified `charly status` surface: for each deployment substrate (pod / vm / local / android) run `charly eval run <bed>` to completion (build → eval image → deploy → eval live → fresh charly update → teardown) on the bed that exercises that substrate, and aggregate a verbatim pass/fail report keyed on the bed\'s `status-shows-*` deploy-scope assertion (the check that proves `charly status --json` reports the right `kind` + nested tree for a live deployment). ALL beds run in PARALLEL via parallel(), bounded by the runtime\'s documented 16-concurrent / 1000-total dynamic-workflow agent ceiling — KVM/libvirt are multi-tenant and podman builds distinct image tags concurrently. A bed skipped for a missing host prereq (libvirt user session for the vm bed, /dev/kvm for the android bed) is logged, never silently dropped.',
  phases: [
    { title: 'Discover', detail: 'emit the substrate→bed map {pod,vm,local,android}' },
    { title: 'Verify', detail: 'charly eval run <bed> per substrate; return verbatim verdict incl. the status-shows-* assertion' },
  ],
}

// The fixed substrate→bed map. Each bed carries a `status-shows-*` deploy-scope
// eval check that asserts `charly status --json` reports the correct `kind` (and,
// for android, the declared pod→android nested tree) for the live deployment.
// One bed per substrate is the unit of coverage — distinct beds get distinct
// container/VM/image names; the author gives each disjoint host ports (the
// loader does NOT check ports — an overlap fails the second bed at deploy),
// so they run concurrent-safe with no worktree.
const SUBSTRATE_BEDS = [
  { substrate: 'pod', bed: 'eval-pod', check: 'status-shows-pod' },
  { substrate: 'vm', bed: 'eval-k3s-vm', check: 'status-shows-vm' },
  { substrate: 'local', bed: 'eval-local', check: 'status-shows-local' },
  { substrate: 'android', bed: 'eval-android-emulator-pod', check: 'status-shows-android-nested' },
]

// Normalize requested substrates/beds: `args` may arrive as an array (Workflow
// tool) or a space-separated string (slash invocation). Empty => all four.
let requested = []
if (Array.isArray(args)) requested = args.filter(Boolean)
else if (typeof args === 'string' && args.trim()) requested = args.trim().split(/\s+/)

let selected = SUBSTRATE_BEDS
if (requested.length) {
  const want = new Set(requested)
  selected = SUBSTRATE_BEDS.filter((e) => want.has(e.substrate) || want.has(e.bed))
}

const DISCOVER_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    beds: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          substrate: { type: 'string', description: 'pod | vm | local | android' },
          bed: { type: 'string' },
          check: { type: 'string', description: 'the status-shows-* deploy-scope assertion id' },
        },
        required: ['substrate', 'bed', 'check'],
      },
    },
  },
  required: ['beds'],
}

const BED_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    bed: { type: 'string' },
    substrate: { type: 'string' },
    exitCode: { type: 'integer', description: '0 pass / 1 infra / 2 checks-failed' },
    ok: { type: 'boolean' },
    skippedPrereq: { type: 'boolean', description: 'true if a host prereq (libvirt/kvm) was missing' },
    statusAssertion: {
      type: 'object',
      additionalProperties: false,
      description: 'verbatim verdict for the status-shows-* deploy-scope check',
      properties: {
        id: { type: 'string' },
        ok: { type: 'boolean' },
        detail: { type: 'string', description: 'the eval-live line for this check (verbatim)' },
      },
      required: ['id', 'ok'],
    },
    failingLogTail: { type: 'string' },
  },
  required: ['bed', 'substrate', 'exitCode', 'ok'],
}

phase('Discover')
// The substrate→bed map is fixed in this workflow; the discover phase emits it
// verbatim (and lets a teammate confirm each bed/check still exists in eval.yml
// before any bed is run). Do NOT run anything in this phase.
const discoverPrompt =
  `Read eval.yml in this project. Confirm each of these substrate→bed→check triples still exists as a kind:eval bed with the named status-shows-* deploy-scope eval check, and return them verbatim as JSON {beds:[{substrate,bed,check}]}. The map is: ` +
  selected.map((e) => `${e.substrate}=>${e.bed} (${e.check})`).join(', ') +
  `. Do NOT run anything.`
const discovered = await agent(discoverPrompt, { schema: DISCOVER_SCHEMA, label: 'discover-status-beds', phase: 'Discover' })
const beds = (discovered && discovered.beds ? discovered.beds : []).filter(Boolean)

if (!beds.length) {
  log('No substrate beds resolved — nothing to verify.')
  return { beds: [], note: 'no substrate beds resolved' }
}

// All beds run in PARALLEL. KVM and libvirt are multi-tenant; podman builds
// distinct image tags concurrently. Simultaneity is bounded by the runtime's
// documented 16-concurrent dynamic-workflow agent ceiling, which queues excess.
log(`Verifying ${beds.length} substrate bed(s) in parallel (bounded + queued by the 16-concurrent runtime ceiling).`)

const runBed = (b) =>
  agent(
    `You are the eval-bed runner verifying the unified \`charly status\` surface for the "${b.substrate}" substrate. Run the kind:eval bed "${b.bed}" EXACTLY as \`charly eval run ${b.bed}\` — do NOT add any flags (no --no-rebuild/--keep/--on-*; that would shrink the R10 spec, CLAUDE.md Law 3.6). The bed's full R10 sequence (build → eval image → deploy → eval live → fresh charly update → teardown) runs the deploy-scope eval check "${b.check}", which asserts that \`charly status --json\` reports the correct substrate kind (and, for android, the declared pod→android nested tree) for the live deployment. Capture stdout/stderr and the process exit code. Read .eval/${b.bed}/<calver>/summary.yml for the per-step verdict, and extract the VERBATIM eval-live line for the "${b.check}" check into statusAssertion. Tail any failing step's .log into failingLogTail. If a required host prereq is missing (libvirt user session for the vm bed, /dev/kvm for the android bed), set skippedPrereq=true and do NOT report it as a pass. Set substrate="${b.substrate}". Return the verbatim verdict — never summarize away a failure.`,
    { schema: BED_SCHEMA, label: `status:${b.substrate}:${b.bed}`, phase: 'Verify' }
  )

phase('Verify')
const all = (await parallel(beds.map((b) => () => runBed(b)))).filter(Boolean)
const passed = all.filter((r) => r.ok && !r.skippedPrereq)
const failed = all.filter((r) => !r.ok && !r.skippedPrereq)
const skipped = all.filter((r) => r.skippedPrereq)
log(`verify-status: ${passed.length} passed, ${failed.length} failed, ${skipped.length} skipped (missing prereq).`)

return {
  total: all.length,
  passed: passed.map((r) => ({ substrate: r.substrate, bed: r.bed, statusAssertion: r.statusAssertion })),
  failed: failed.map((r) => ({ substrate: r.substrate, bed: r.bed, exitCode: r.exitCode, statusAssertion: r.statusAssertion, failingLogTail: r.failingLogTail })),
  skipped: skipped.map((r) => ({ substrate: r.substrate, bed: r.bed })),
  beds: all,
}
