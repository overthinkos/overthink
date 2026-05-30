export const meta = {
  name: 'verify-beds',
  description:
    'R10 fan-out over the existing kind:eval disposable beds: run `ov eval run <bed>` to completion (build → eval image → deploy → eval live → fresh ov update → teardown) and aggregate a verbatim pass/fail report. This is the DEDICATED verification phase — run it AFTER every implementation task is complete, never as a parallel/background track during a cutover (CLAUDE.md Law 5). Each bed run saturates the host (image build, single-tenant KVM/libvirt), so only no-build local beds run concurrently; image-building pod beds and VM/KVM beds run sequentially.',
  phases: [
    { title: 'Discover', detail: 'enumerate kind:eval beds + their target kind' },
    { title: 'Run beds', detail: 'ov eval run <bed> per bed; return verbatim verdict' },
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
  ? `Read eval.yml and every image/*/overthink.yml in this project. For each of these bed names: ${requested.join(', ')} — return its kind:eval target kind (pod/vm/local). Return JSON {beds:[{bed,target}]}. Do NOT run anything.`
  : 'Read eval.yml and every image/*/overthink.yml in this project. Return ALL kind:eval bed entities as JSON {beds:[{bed,target}]} where target is the entity .target field (pod/vm/local). Do NOT run anything.'
const discovered = await agent(discoverPrompt, { schema: DISCOVER_SCHEMA, label: 'discover-beds', phase: 'Discover' })
const beds = (discovered && discovered.beds ? discovered.beds : []).filter(Boolean)

if (!beds.length) {
  log('No kind:eval beds discovered — nothing to verify.')
  return { beds: [], note: 'no beds discovered' }
}

// Resource split: only no-build `local` beds run concurrently. Anything that
// builds an image (`pod`) or uses single-tenant KVM/libvirt (`vm`, android)
// runs sequentially to avoid build-cache/disk/KVM contention (R4).
const isSerial = (b) => b.target === 'pod' || b.target === 'vm' || /android|kvm/i.test(b.bed)
const serial = beds.filter(isSerial)
const concurrent = beds.filter((b) => !isSerial(b))
log(`Discovered ${beds.length} bed(s): ${concurrent.length} concurrent (local), ${serial.length} sequential (build/VM/KVM).`)

const runBed = (b) =>
  agent(
    `You are the eval-bed runner. Run the kind:eval bed "${b.bed}" EXACTLY as \`ov eval run ${b.bed}\` — do NOT add any flags (no --no-rebuild/--keep/--on-*; that would shrink the R10 spec, CLAUDE.md Law 3.6). Capture stdout/stderr and the process exit code. Then read .eval/${b.bed}/<calver>/summary.yml for the per-step verdict and tail any failing step's .log. If a required host prereq is missing (libvirt user session for a vm bed, /dev/kvm for the android bed), set skippedPrereq=true and do NOT report it as a pass. Return the verbatim verdict — never summarize away a failure.`,
    { schema: BED_SCHEMA, label: `bed:${b.bed}`, phase: 'Run beds' }
  )

phase('Run beds')
const concurrentResults = concurrent.length ? await parallel(concurrent.map((b) => () => runBed(b))) : []
const serialResults = []
for (const b of serial) {
  serialResults.push(await runBed(b))
}

const all = [...concurrentResults, ...serialResults].filter(Boolean)
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
