// Shared CUE definitions referenced by multiple kinds. R3: define each shared
// shape ONCE here, not per-kind. All schema/*.cue files compile into one
// instance (no package clauses), so any kind def can reference these directly.

// Execution context for a plan step.
#Context: "build" | "deploy" | "runtime"

// One flat plan step: exactly ONE intent keyword (run / check / agent-run /
// agent-check / include) carrying prose, plus an inline Op (verb + modifiers).
// Exactly-one-keyword is the discriminated-union idiom — each arm requires its
// keyword and forbids the other four via _|_. The Op modifier tail stays OPEN
// (...) for now; the full Op vocabulary is tightened in a later step.
#Step:
	{run: string & !="", check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_, ...} |
	{check: string & !="", run?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, include?: _|_, ...} |
	{"agent-run": string & !="", run?: _|_, check?: _|_, "agent-check"?: _|_, include?: _|_, ...} |
	{"agent-check": string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, include?: _|_, ...} |
	{include: string & !="", run?: _|_, check?: _|_, "agent-run"?: _|_, "agent-check"?: _|_, ...}

// A BuildKit cache mount (shared by distro + builder). dst is the absolute
// in-builder cache path; sharing is the BuildKit sharing mode; owned renders a
// uid/gid-owned (user-writable) cache instead of the shared/locked root form.
#CacheMount: {
	dst:      string & =~"^/"
	sharing?: *"locked" | "shared" | "private"
	owned?:   bool
	...
}

// Three-phase template set (shared by distro formats + builders): prepare →
// install → cleanup, each with a container (Containerfile) and host (shell)
// rendering. Template bodies are Go text/template — strings, never parsed here.
#PhaseSet: {
	prepare?: #PhaseTemplates
	install?: #PhaseTemplates
	cleanup?: #PhaseTemplates
	...
}
#PhaseTemplates: {
	container?: string
	host?:      string
	...
}
