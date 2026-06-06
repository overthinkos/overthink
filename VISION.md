# Overthink — The Vision

*The thesis behind the candy store.*

Most other tools hand an agent a sandbox — and then spend their effort taking
things *away*: fewer commands, no network, no installs, safety bought by
stripping the candy out, and most of the capability lost with it. Overthink does
the opposite. It builds a **box** with real, kernel-enforced walls, throws the
doors open, and fills the room with the **whole candy store** — every tool, every
candy imaginable, a real registry, a real GPU, and the whole factory standing by
to make even better candy. Secure the room, not the candy. That one inversion is
the whole bet, and everything below pours out of it.

## The tenets

1. **Secure the room, not the candy.** Safety lives at the boundary of a
   candybox — rootless containers, KVM-isolated VMs, encrypted volumes — never in
   a shrunken toolset. A walled room you can hand over *completely* beats an empty
   sandbox you keep narrowing.
   → CLAUDE.md "Candyboxing".

2. **One recipe, many boxes.** A single declarative recipe — candies stacked into
   a box — pours into every mold: an interactive shell, a managed pod, a host
   workstation, a k8s cluster, a bootable VM, an Android device. Write the recipe
   once; let `ov` set it in whatever shape the moment needs.
   → README "Build → run → deploy → evaluate".

3. **Every candy ships with its recipe card.** Every candy, image, and verb
   carries a dedicated skill, so nothing in the candy store is a mystery — neither
   human nor agent ever has to guess what a piece does, how it's made, or how it
   should taste.
   → `plugins/README.md`.

4. **Two tasters at one bench.** The same `ov` surface — every verb, mirrored
   through an MCP gateway — serves the human at the keyboard and the agent driving
   the line, with no second-class channel for either. Built for you *and* your
   agents, in the same breath.
   → CLAUDE.md "Candyboxing", `/ov-internals:agents`.

5. **Taste every batch before it ships — Risk Driven Development.** Recipe cards
   drift and vats spring leaks, so nothing high-stakes rides on "the card says
   so." The riskiest question — *do these candies actually melt together at
   today's versions?* — gets proven on a real, disposable candybox first. Reality
   is the only ground truth. Risk Driven Development decides *what* to prove, and
   *when*: the riskiest unknown, first.
   → CLAUDE.md "Risk Driven Development (RDD)", `/ov-eval:eval`.

6. **Write down what "good" means, and have an agent taste it — Agent Driven
   Development.** What a candy should be is captured as runnable acceptance
   scenarios, not left to opinion. A checklist verifies the measurable; for the
   subtle "is it actually right?" an agent tastes the live batch with the full
   probe kit and judges. The recipe IS the test — and an agent is a first-class
   author of it and a first-class grader of it. Agent Driven Development is Risk
   Driven Development's co-equal twin: RDD proves the risky assumptions a behavior
   rests on; ADD pins down what correct behavior *is* and drives — human or agent
   — until the live batch passes.
   → CLAUDE.md "Agent Driven Development (ADD)", `/ov-eval:eval`.

7. **Conched smooth — pass after pass until silk.** Running the loop once proves a
   candybox works; running the build → run → deploy → evaluate mantra *over and
   over* makes it good. Like conching chocolate, every pass grinds the candy
   smoother — deduplicated, ordered, cache-warmed, dead code and band-aid fixes
   cleared, re-proven on a live deployment — until the build is reproducible, a
   no-op rebuild is free, and the box is silk. The first box released for public
   consumption should taste like the finest milk chocolate, not a rock sprayed
   with quick-drying brown paint.
   → README "Build", "Build → run → deploy → evaluate", `/ov-build:build`.

8. **A spoiled batch costs one rebuild.** All that conching is cheap because a
   candybox is throwaway by explicit design: a wrong move is a single `ov update`,
   not an incident. That is exactly what lets autonomous iteration be *fearless*
   and *safe* at once — disposability is the license to be bold.
   → CLAUDE.md "Disposable-Only Autonomy", `/ov-internals:disposable`.

9. **Free to forge a better candybox.** And when the box itself is wrong — wrong
   layers, a missing candy, a composition that won't melt together — the agent is
   always free to build a new and better one, never to make do with the wrong
   room. A candybox is just another recipe, and a throwaway one, so forging a
   fresh box costs no more than patching the wrong one. The freedom to build the
   *right* box is what makes the whole candy store usable.
   → CLAUDE.md "Candyboxing", "Disposable-Only Autonomy", `/ov-internals:disposable`.

## Where the factory is heading

- **Widen what one recipe can become.** The same declaration already pours into
  containers, VMs, k8s, hosts, and Android — the long arc is *more molds under
  one wrapper*, never more wrappers to learn.
- **Hand the whole line to the agents.** The full build → deploy → prove →
  iterate loop run end-to-end *inside* the candybox — agents free to forge a
  fresh, better box whenever the job needs one — with the human watching the floor
  rather than turning every crank.
- **Verification becomes the cadence, not a checkpoint.** The long arc of *prove
  it first* (Risk Driven Development) and *the spec is the test* (Agent Driven
  Development) is a single loop: the agent writes down what a good candy is as
  runnable scenarios, proves the riskiest unknowns on a live, disposable candybox
  *before* it commits to a recipe, and grades its own acceptance against the
  running box — until *never trust, verify* is the factory's default rhythm, woven
  through every batch, not a discipline anyone has to remember to apply.
  → CLAUDE.md "Risk Driven Development (RDD)" + "Agent Driven Development (ADD)".
- **A shared candy store.** Cross-repo, versioned candies and boxes (`@github`
  refs, content-derived versions) maturing into an ecosystem you *compose from*,
  not a pantry you restock by hand.
- **The long bet.** As agents grow more capable, the winning environment is a
  fully-stocked candy store, not a tighter sandbox. Overthink is built for that
  world — and built to still feel like a chocolate factory when it arrives.

---

*The factory floor is documented in [README.md](README.md); the house rules in
[CLAUDE.md](CLAUDE.md); every candy and box has a recipe card in
[plugins/README.md](plugins/README.md). Dated history lives — and only lives —
in [CHANGELOG.md](CHANGELOG.md).*
