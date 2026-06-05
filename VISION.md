# Overthink — The Vision

*The thesis behind the candy store.*

Every other tool hands your agent a padded cell: fewer commands, no network,
no installs — safety bought by taking the candy away, and most of the
capability lost with it. Overthink does the opposite. It builds a **box** with
real, kernel-enforced walls, throws the doors open, and fills the room with the
**whole candy store** — every tool, every layer, a real registry, a real GPU.
Secure the room, not the candy. That one inversion is the whole bet, and
everything below pours out of it.

## The tenets

1. **Secure the room, not the candy.** Safety lives at the boundary of a
   disposable box — rootless containers, KVM-isolated VMs, encrypted volumes —
   never in a shrunken toolset. A walled room you can hand over *completely*
   beats a cell you have to keep narrowing.
   → CLAUDE.md "Candyboxing".

2. **One recipe, many boxes.** A single declarative recipe — candies stacked
   into a box — pours into every mold: an interactive shell, a managed pod, a
   host workstation, a k8s cluster, a bootable VM, an Android device. Write the
   recipe once; let `ov` set it in whatever shape the moment needs.
   → README "Build → run → deploy → evaluate".

3. **Two tasters at one bench.** The same `ov` surface — every verb, mirrored
   through an MCP gateway — serves the human at the keyboard and the agent
   driving the line, with no second-class channel for either. Built for you
   *and* your agents, in the same breath.
   → CLAUDE.md "Candyboxing", `/ov-internals:agents`.

4. **Taste every batch before it ships.** Recipe cards drift and vats spring
   leaks, so nothing high-stakes rides on "the card says so." The riskiest
   question — *do these candies actually melt together at today's versions?* —
   gets proven on a real, disposable batch first. Reality is the only ground
   truth.
   → CLAUDE.md "Risk Driven Development (RDD)", `/ov-eval:eval`.

5. **Every batch carries its tasting notes — and an agent to taste.** What a
   candy should be is written down as runnable acceptance scenarios, not left to
   opinion. A checklist verifies the measurable; for the subtle "is it actually
   right?" an agent tastes the live batch with the full probe kit and judges.
   The recipe IS the test — and an agent both writes it and grades it.
   → CLAUDE.md "Agent Driven Development (ADD)", `/ov-eval:eval`.

6. **A spoiled batch costs one rebuild.** Because the box is throwaway by
   explicit design, a wrong move is a single `ov update`, not an incident —
   which is exactly what lets autonomous iteration be *fearless* and *safe* at
   once. Disposability is the licence to be bold.
   → CLAUDE.md "Disposable-Only Autonomy", `/ov-internals:disposable`.

7. **Conched smooth, cached warm.** Like conching chocolate, the planner grinds
   every candy smooth — deduplicated, ordered, cache-warmed — before it sets
   into a box, so a build is reproducible and a no-op rebuild is free.
   → README "Build", `/ov-build:build`.

8. **Every candy ships with its recipe card.** Every layer, image, and verb
   carries a dedicated skill, so neither human nor agent ever has to guess
   what's in the vat before reaching in.
   → `plugins/README.md`.

## Where the factory is heading

- **Widen what one recipe can become.** The same declaration already pours into
  containers, VMs, k8s, hosts, and Android — the long arc is *more molds under
  one wrapper*, never more wrappers to learn.
- **Hand the whole line to the agents.** Richer build → deploy → prove →
  iterate loops the agent runs end-to-end *inside* the candybox, with the human
  watching the floor rather than turning every crank.
- **Verification becomes the cadence, not a checkpoint.** The long arc of
  *prove it first* (Risk Driven Development) and *the spec is the test* (Agent
  Driven Development) is a single loop: the agent writes down what a good candy
  is as runnable scenarios, proves the riskiest unknowns on a live, disposable
  batch *before* it commits to a recipe, and grades its own acceptance against
  the running box — until *never trust, verify* is the factory's default rhythm,
  woven through every batch, not a discipline anyone has to remember to apply.
  → CLAUDE.md "Risk Driven Development (RDD)" + "Agent Driven Development (ADD)".
- **A shared candy store.** Cross-repo, versioned candies and boxes (`@github`
  refs, content-derived versions) maturing into an ecosystem you *compose from*,
  not a pantry you restock by hand.
- **The long bet.** As agents grow more capable, the winning environment is a
  fully-stocked disposable box, not a tighter cage. Overthink is built for that
  world — and built to still feel like a chocolate factory when it arrives.

---

*The factory floor is documented in [README.md](README.md); the house rules in
[CLAUDE.md](CLAUDE.md); every candy and box has a recipe card in
[plugins/README.md](plugins/README.md). Dated history lives — and only lives —
in [CHANGELOG.md](CHANGELOG.md).*
