export const meta = {
  name: 'audit-deploy-configs',
  description:
    'Evaluate deployment configs — for AI agents and human operators. Runs `charly box validate` once (config correctness), then per target a read-only deploy-verifier pass (`charly eval box` for built images, `charly eval live` + `charly status` for running deploys) and aggregates a health report. NON-destructive: never builds, deploys, rebuilds, or tears down. For the destructive R10 bed gate use /verify-beds instead.',
  phases: [
    { title: 'Validate', detail: 'charly box validate — config + warnings' },
    { title: 'Discover', detail: 'enumerate target images/deploys to audit' },
    { title: 'Audit', detail: 'per-target read-only eval image/live + status' },
  ],
}

let requested = []
if (Array.isArray(args)) requested = args.filter(Boolean)
else if (typeof args === 'string' && args.trim()) requested = args.trim().split(/\s+/)

const VALIDATE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    ok: { type: 'boolean' },
    warnings: { type: 'array', items: { type: 'string' } },
    errors: { type: 'array', items: { type: 'string' } },
  },
  required: ['ok'],
}

const DISCOVER_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    targets: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          name: { type: 'string' },
          kind: { type: 'string', description: 'image | deploy' },
        },
        required: ['name', 'kind'],
      },
    },
  },
  required: ['targets'],
}

const HEALTH_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    name: { type: 'string' },
    probes: { type: 'array', items: { type: 'string' } },
    result: { type: 'string', description: 'healthy | DEGRADED | NOT-RUNNING | CHECKS-FAILED | NOT-BUILT' },
    passed: { type: 'integer' },
    failed: { type: 'integer' },
    skipped: { type: 'integer' },
    failingChecks: { type: 'array', items: { type: 'string' } },
  },
  required: ['name', 'result'],
}

// Phase 1 — config correctness (cheap, no build). A warning is not a pass
// (CLAUDE.md zero-warnings gate) — surface them so the caller can clear them.
phase('Validate')
const validation = await agent(
  'Run `charly box validate` in this project. Return {ok, warnings, errors} — list every warning and error verbatim. Do not build or deploy anything.',
  { schema: VALIDATE_SCHEMA, label: 'charly box validate', phase: 'Validate' }
)
if (validation && validation.warnings && validation.warnings.length) {
  log(`charly box validate: ${validation.warnings.length} warning(s) — these block a clean R10 (zero-warnings gate).`)
}

// Phase 2 — what to audit. Explicit args win; otherwise enumerate enabled images.
phase('Discover')
let targets
if (requested.length) {
  targets = requested.map((name) => ({ name, kind: 'box' }))
} else {
  const discovered = await agent(
    'Read charly.yml in this project. Return JSON {targets:[{name,kind}]} listing the ENABLED box short-names (kind "box") and any deploy names from a local deploy.yml (kind "deploy"). Do NOT run or build anything.',
    { schema: DISCOVER_SCHEMA, label: 'discover-targets', phase: 'Discover' }
  )
  targets = (discovered && discovered.targets ? discovered.targets : []).filter(Boolean)
}

if (!targets.length) {
  log('No deploy targets discovered — only charly box validate ran.')
  return { validation, targets: [] }
}
log(`Auditing ${targets.length} deploy target(s).`)

// Phase 3 — per-target read-only health. These probes don't build, so fan out.
phase('Audit')
const results = (
  await parallel(
    targets.map((t) => () =>
      agent(
        `You are the deploy-verifier (read-only — never build/deploy/rebuild/destroy). Evaluate the deploy config "${t.name}" (kind ${t.kind}). Run the probes that apply: \`charly eval box ${t.name}\` if a built image exists locally (use the full registry ref if the short name is ambiguous; report NOT-BUILT if absent), and \`charly eval live ${t.name}\` + \`charly status ${t.name}\` if it is currently deployed (report NOT-RUNNING if not). Return the health verdict with verbatim failing-check output — never hide a failed check.`,
        { schema: HEALTH_SCHEMA, label: `audit:${t.name}`, phase: 'Audit' }
      )
    )
  )
).filter(Boolean)

const unhealthy = results.filter((r) => r.result === 'CHECKS-FAILED' || r.result === 'DEGRADED')
log(`audit-deploy-configs: ${results.length} audited, ${unhealthy.length} with failing checks.`)

return {
  validation,
  audited: results.length,
  unhealthy: unhealthy.map((r) => ({ name: r.name, result: r.result, failingChecks: r.failingChecks })),
  targets: results,
}
