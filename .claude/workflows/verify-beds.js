export const meta = {
  name: 'verify-beds',
  description:
    'Full-live-test commit-gate fan-out over the existing kind:eval disposable beds, also usable for continuous verification throughout development: run `charly eval run <bed>` to completion (build → eval image → deploy → eval live → fresh charly update → teardown) and aggregate a verbatim pass/fail report. ALL beds run in PARALLEL via parallel(), bounded by the runtime’s documented 16-concurrent / 1000-total dynamic-workflow agent ceiling — KVM/libvirt are multi-tenant and podman builds distinct image tags concurrently. Beds skipped for a missing host prereq (libvirt session for a vm bed, /dev/kvm for android) are logged, never silently dropped.',
  phases: [
    { title: 'Discover', detail: 'enumerate kind:eval beds + their target kind' },
    { title: 'Run beds', detail: 'charly eval run <bed> per bed; return verbatim verdict' },
  ],
}

// Normalize requested beds: Workflow `args` may arrive as an array (Workflow
// tool) or a space-separated string (slash invocation). Empty => all beds.
let requested = []
if (Array.isArray(args)) requested = args.filter(Boolean)
else if (typeof args === 'string' && args.trim()) requested = args.trim().split(/\s+/)

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
          bed: { type: 'string' },
          target: { type: 'string', description: 'pod | vm | local' },
        },
        required: ['bed', 'target'],
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
    exitCode: { type: 'integer', description: '0 pass / 1 infra / 2 checks-failed' },
    ok: { type: 'boolean' },
    skippedPrereq: { type: 'boolean', description: 'true if a host prereq (libvirt/kvm) was missing' },
    steps: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          name: { type: 'string' },
          ok: { type: 'boolean' },
          durationSeconds: { type: 'number' },
        },
        required: ['name', 'ok'],
      },
    },
    failingLogTail: { type: 'string' },
  },
  required: ['bed', 'exitCode', 'ok'],
}

phase('Discover')
const discoverPrompt = requested.length
  ? `Read eval.yml and every image/*/charly.yml in this project. For each of these bed names: ${requested.join(', ')} — return its kind:eval target kind (pod/vm/local). Return JSON {beds:[{bed,target}]}. Do NOT run anything.`
  : 'Read eval.yml and every image/*/charly.yml in this project. Return ALL kind:eval bed entities as JSON {beds:[{bed,target}]} where target is the entity .target field (pod/vm/local). Do NOT run anything.'
const discovered = await agent(discoverPrompt, { schema: DISCOVER_SCHEMA, label: 'discover-beds', phase: 'Discover' })
const beds = (discovered && discovered.beds ? discovered.beds : []).filter(Boolean)

if (!beds.length) {
  log('No kind:eval beds discovered — nothing to verify.')
  return { beds: [], note: 'no beds discovered' }
}

// All beds run in PARALLEL. KVM and libvirt are multi-tenant; podman builds
// distinct image tags concurrently. Simultaneity is bounded by the runtime's
// documented 16-concurrent dynamic-workflow agent ceiling, which queues excess.
log(`Discovered ${beds.length} bed(s): running all in parallel (bounded + queued by the 16-concurrent runtime ceiling).`)

const runBed = (b) =>
  agent(
    `You are the eval-bed runner. Run the kind:eval bed "${b.bed}" EXACTLY as \`charly eval run ${b.bed}\` — do NOT add any flags (no --no-rebuild/--keep/--on-*; that would shrink the R10 spec, CLAUDE.md Law 3.6). Capture stdout/stderr and the process exit code. Then read .eval/${b.bed}/<calver>/summary.yml for the per-step verdict and tail any failing step's .log. If a required host prereq is missing (libvirt user session for a vm bed, /dev/kvm for the android bed), set skippedPrereq=true and do NOT report it as a pass. Return the verbatim verdict — never summarize away a failure.`,
    { schema: BED_SCHEMA, label: `bed:${b.bed}`, phase: 'Run beds' }
  )

phase('Run beds')
const all = (await parallel(beds.map((b) => () => runBed(b)))).filter(Boolean)
const passed = all.filter((r) => r.ok && !r.skippedPrereq)
const failed = all.filter((r) => !r.ok && !r.skippedPrereq)
const skipped = all.filter((r) => r.skippedPrereq)
log(`verify-beds: ${passed.length} passed, ${failed.length} failed, ${skipped.length} skipped (missing prereq).`)

return {
  total: all.length,
  passed: passed.map((r) => r.bed),
  failed: failed.map((r) => ({ bed: r.bed, exitCode: r.exitCode, failingLogTail: r.failingLogTail })),
  skipped: skipped.map((r) => r.bed),
  beds: all,
}
