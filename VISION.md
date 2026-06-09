# OpenCharly — The Vision

*The thesis behind Charly.*

Most other tools hand an agent a sandbox — and then spend their effort taking things *away*: fewer commands, no network, no installs, safety bought by stripping the candy out, and most of the capability lost with it. Charly does the opposite. It builds a **candybox** with real, kernel-enforced walls, throws the doors open, and fills the room with the **whole candy store**.

## The tenets

1. **Secure the room, not the candy.** Safety lives at the boundary of a candybox — rootless containers, KVM-isolated VMs, encrypted volumes — never in a shrunken toolset. A walled room you can hand over *completely* beats an empty sandbox you keep narrowing.
   → CLAUDE.md "Candyboxing".

2. **One recipe, many boxes.** A single declarative recipe — candies stacked into a box — pours into every mold: an interactive shell, a managed pod, a host workstation, a k8s cluster, a bootable VM, an Android device. Write the recipe once; let `charly` set it in whatever shape the moment needs.
   → README "Build → run → deploy → evaluate".

3. **Every candy ships with its recipe card.** Every candy, image, and verb carries a dedicated skill, so nothing in the candy store is a mystery — neither human nor agent ever has to guess what a piece does, how it's made, or how it should taste.
   → `plugins/README.md`.

4. **Two tasters at one bench.** The same `charly` surface serves the human at the keyboard and the agent driving the line, with no second-class channel for either. Built for you *and* your agents, in the same breath.
   → CLAUDE.md "Candyboxing", `/charly-internals:agents`.

5. **Taste every batch before it ships — Risk Driven Development.** Recipe cards drift and vats spring leaks, so nothing high-stakes should ride on "the card says so." The riskiest question — *do these candies actually melt together at today's versions?* — gets proven on a real, disposable candybox first. Reality is the only ground truth. Risk Driven Development decides *what* to prove, and *when*: the riskiest unknown, first.
   → CLAUDE.md "Risk Driven Development (RDD)", `/charly-eval:eval`.

6. **Write down what "good" means, and have an agent taste it — Agent Driven Evaluation.** What a candybox should be is captured as runnable acceptance scenarios, not left to opinion. A checklist verifies the measurable; for the subtle "is it actually right?" an agent tastes the live batch with the full probe kit and judges. The recipe IS the test — and an agent is a first-class author of it and a first-class grader of it. Agent Driven Evaluation is Risk Driven Development's co-equal twin: RDD proves the risky assumptions a behavior rests on; ADE pins down what correct behavior *is* and grades the live batch against it.
   → CLAUDE.md "Agent Driven Evaluation (ADE)", `/charly-eval:eval`.

7. **Conched smooth — pass after pass until silk.** Running the eval loop down to the micrometer proves a candybox works; running the build → run → deploy → evaluate mantra *over and over again* makes it good. Like conching chocolate, every pass grinds the candy smoother — deduplicated, refined, dead code and band-aid fixes removed, yet another round of evals added, re-proven on a live deployment — until the candybox has proven itself and the candy is silk. The first box released for public consumption should taste like the finest milk chocolate ever, not like a rock sprayed with quick-drying brown paint.
   → README "Build", "Build → run → deploy → evaluate", `/charly-build:build`.

8. **A spoiled batch costs one rebuild.** All that conching is cheap because a candybox is throwaway by explicit design: a wrong move is just another lesson learned after a full root cause analysis of the underlying issues, not an incident that needs to be prevented at all costs in the future. That is exactly what lets autonomous iteration be *fearless* and *safe* at once — disposability is the license to be bold.
   → CLAUDE.md "Disposable-Only Autonomy", `/charly-internals:disposable`.

9. **Free to forge a better candybox.** And when the box itself is wrong — wrong layers, a missing candy, a composition that won't melt together — the agent is always free to build a new and better one, never to make do with the wrong room. A candybox is just another recipe, and a throwaway one, so forging a fresh box costs no more than patching the wrong one. The freedom to build the *right* box is what makes the whole candy store usable.
   → CLAUDE.md "Candyboxing", "Disposable-Only Autonomy", `/charly-internals:disposable`.

## Where the factory is heading

- **Widen what one recipe can become.** The same declaration already pours into containers, VMs, k8s, hosts, and Android — the long arc is *more molds under one wrapper*, never more wrappers to learn.
- **Hand the whole line to the agents.** The full build → deploy → prove → iterate loop run end-to-end *inside* the candybox — agents free to forge a fresh, better box whenever the job needs one — with the human watching the floor rather than turning every crank.
- **Verification becomes the cadence, not a checkpoint.** The long arc of *prove it first* (Risk Driven Development) and *the spec is the test* (Agent Driven Evaluation) is a single loop: the agent writes down what a good candy is as runnable scenarios, proves the riskiest unknowns on a live, disposable candybox *before* it commits to a recipe, and grades its own acceptance against the running box — until *never trust, verify* is the factory's default rhythm, woven through every batch, not a discipline anyone has to remember to apply.
  → CLAUDE.md "Risk Driven Development (RDD)" + "Agent Driven Evaluation (ADE)".
- **A shared candy store.** Cross-repo, versioned candies and boxes (`@github` refs, content-derived versions) maturing into an ecosystem you *compose from*, not a pantry you restock by hand.
- **The long bet.** As agents grow more capable, the winning environment is a fully-stocked candy store, not a tighter sandbox. Charly is built for that world — and built to still feel like a magic chocolate factory when it arrives.

---

*The factory floor is documented in [README.md](README.md); the house rules in [CLAUDE.md](CLAUDE.md); every candy and box has a recipe card in [plugins/README.md](plugins/README.md). Dated history lives — and only lives — in [CHANGELOG.md](CHANGELOG.md).*
