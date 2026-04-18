package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Check is a single declarative test entry. Exactly one verb discriminator
// field must be non-empty. Mirrors the shape of Task in layers.go:219 —
// list-of-discriminators is the project's idiomatic style for declarative
// YAML entries.
//
// Authoring examples:
//
//	tests:
//	  - file: /usr/bin/redis-server
//	    exists: true
//	    mode: "0755"
//	  - port: 6379
//	    listening: true
//	  - command: redis-cli ping
//	    stdout: PONG
//	  - http: http://127.0.0.1:8888/api
//	    status: 200
type Check struct {
	// Verb discriminators — exactly one non-empty (enforced by Kind()).
	File        string `yaml:"file,omitempty"          json:"file,omitempty"`
	Package     string `yaml:"package,omitempty"       json:"package,omitempty"`
	Service     string `yaml:"service,omitempty"       json:"service,omitempty"`
	Port        int    `yaml:"port,omitempty"          json:"port,omitempty"`
	Process     string `yaml:"process,omitempty"       json:"process,omitempty"`
	Command     string `yaml:"command,omitempty"       json:"command,omitempty"`
	HTTP        string `yaml:"http,omitempty"          json:"http,omitempty"`
	DNS         string `yaml:"dns,omitempty"           json:"dns,omitempty"`
	User        string `yaml:"user,omitempty"          json:"user,omitempty"`
	Group       string `yaml:"group,omitempty"         json:"group,omitempty"`
	Interface   string `yaml:"interface,omitempty"     json:"interface,omitempty"`
	KernelParam string `yaml:"kernel-param,omitempty"  json:"kernel-param,omitempty"`
	Mount       string `yaml:"mount,omitempty"         json:"mount,omitempty"`
	Addr        string `yaml:"addr,omitempty"          json:"addr,omitempty"`
	Matching    any    `yaml:"matching,omitempty"      json:"matching,omitempty"`

	// Shared modifiers
	ID          string `yaml:"id,omitempty"           json:"id,omitempty"`
	Description string `yaml:"description,omitempty"  json:"description,omitempty"`
	Skip        bool   `yaml:"skip,omitempty"         json:"skip,omitempty"`
	Timeout     string `yaml:"timeout,omitempty"      json:"timeout,omitempty"`
	Scope       string `yaml:"scope,omitempty"        json:"scope,omitempty"` // "build" | "deploy" (default filled by collector)
	InContainer *bool  `yaml:"in_container,omitempty" json:"in_container,omitempty"`

	// Origin is populated at collection time (layer:<name>, image:<name>,
	// deploy-default, deploy-local). Not authored in YAML, but travels in
	// the OCI label JSON.
	Origin string `yaml:"-" json:"origin,omitempty"`

	// file-specific
	Exists   *bool       `yaml:"exists,omitempty"   json:"exists,omitempty"`
	Mode     string      `yaml:"mode,omitempty"     json:"mode,omitempty"`
	Owner    string      `yaml:"owner,omitempty"    json:"owner,omitempty"`
	GroupOf  string      `yaml:"group_of,omitempty" json:"group_of,omitempty"` // file's group; named to avoid clashing with verb-level Group
	Filetype string      `yaml:"filetype,omitempty" json:"filetype,omitempty"` // file, directory, symlink
	Contains MatcherList `yaml:"contains,omitempty" json:"contains,omitempty"`
	Sha256   string      `yaml:"sha256,omitempty"   json:"sha256,omitempty"`

	// package-specific
	Installed *bool    `yaml:"installed,omitempty" json:"installed,omitempty"`
	Versions  []string `yaml:"versions,omitempty"  json:"versions,omitempty"`

	// service-specific
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Running *bool `yaml:"running,omitempty" json:"running,omitempty"` // also reused by process

	// port-specific
	Listening *bool  `yaml:"listening,omitempty" json:"listening,omitempty"`
	IP        string `yaml:"ip,omitempty"        json:"ip,omitempty"`

	// command-specific
	ExitStatus *int        `yaml:"exit_status,omitempty" json:"exit_status,omitempty"`
	Stdout     MatcherList `yaml:"stdout,omitempty"      json:"stdout,omitempty"`
	Stderr     MatcherList `yaml:"stderr,omitempty"      json:"stderr,omitempty"`

	// http-specific
	Status        int         `yaml:"status,omitempty"              json:"status,omitempty"`
	Body          MatcherList `yaml:"body,omitempty"                json:"body,omitempty"`
	Headers       MatcherList `yaml:"headers,omitempty"             json:"headers,omitempty"`
	AllowInsecure bool        `yaml:"allow_insecure,omitempty"      json:"allow_insecure,omitempty"`
	NoFollowRedir bool        `yaml:"no_follow_redirects,omitempty" json:"no_follow_redirects,omitempty"`
	CAFile        string      `yaml:"ca_file,omitempty"             json:"ca_file,omitempty"`
	Method        string      `yaml:"method,omitempty"              json:"method,omitempty"`
	RequestBody   string      `yaml:"request_body,omitempty"        json:"request_body,omitempty"`

	// dns-specific
	Resolvable *bool    `yaml:"resolvable,omitempty" json:"resolvable,omitempty"`
	Addrs      []string `yaml:"addrs,omitempty"      json:"addrs,omitempty"` // also reused by interface
	Server     string   `yaml:"server,omitempty"     json:"server,omitempty"`

	// user/group-specific
	UID    *int     `yaml:"uid,omitempty"    json:"uid,omitempty"`
	GID    *int     `yaml:"gid,omitempty"    json:"gid,omitempty"`
	Home   string   `yaml:"home,omitempty"   json:"home,omitempty"`
	Shell  string   `yaml:"shell,omitempty"  json:"shell,omitempty"`
	Groups []string `yaml:"groups,omitempty" json:"groups,omitempty"`

	// interface-specific
	MTU *int `yaml:"mtu,omitempty" json:"mtu,omitempty"`

	// kernel-param specific (and reused wherever a single expected value
	// + optional matcher decoration is the natural shape). MatcherList
	// scalar shorthand means `value: "1"` decodes to [{equals 1}].
	Value MatcherList `yaml:"value,omitempty" json:"value,omitempty"`

	// mount-specific
	MountSource string      `yaml:"mount_source,omitempty" json:"mount_source,omitempty"` // backing device/path
	Filesystem  string      `yaml:"filesystem,omitempty"   json:"filesystem,omitempty"`
	Opts        MatcherList `yaml:"opts,omitempty"         json:"opts,omitempty"`

	// addr-specific
	Reachable *bool `yaml:"reachable,omitempty" json:"reachable,omitempty"`

	// command-specific routing (false = run from host, true = run via podman exec)
	FromHost bool `yaml:"from_host,omitempty" json:"from_host,omitempty"`
}

// CheckVerbs lists valid discriminator keys in stable order (used for
// deterministic error messages).
var CheckVerbs = []string{
	"file", "package", "service", "port", "process", "command",
	"http", "dns", "user", "group", "interface", "kernel-param",
	"mount", "addr", "matching",
}

// Kind returns the check's verb name and an error if zero or multiple
// verb discriminators are set. Matches Task.Kind() semantics.
func (c *Check) Kind() (string, error) {
	set := c.verbsSet()
	if len(set) == 0 {
		return "", fmt.Errorf("check has no verb set (expected exactly one of: %s)", strings.Join(CheckVerbs, ", "))
	}
	if len(set) > 1 {
		return "", fmt.Errorf("check has multiple verbs set (%s); exactly one is required", strings.Join(set, ", "))
	}
	return set[0], nil
}

// verbsSet returns the list of verb discriminators that are currently non-zero.
func (c *Check) verbsSet() []string {
	var set []string
	if c.File != "" {
		set = append(set, "file")
	}
	if c.Package != "" {
		set = append(set, "package")
	}
	if c.Service != "" {
		set = append(set, "service")
	}
	if c.Port != 0 {
		set = append(set, "port")
	}
	if c.Process != "" {
		set = append(set, "process")
	}
	if c.Command != "" {
		set = append(set, "command")
	}
	if c.HTTP != "" {
		set = append(set, "http")
	}
	if c.DNS != "" {
		set = append(set, "dns")
	}
	if c.User != "" {
		set = append(set, "user")
	}
	if c.Group != "" {
		set = append(set, "group")
	}
	if c.Interface != "" {
		set = append(set, "interface")
	}
	if c.KernelParam != "" {
		set = append(set, "kernel-param")
	}
	if c.Mount != "" {
		set = append(set, "mount")
	}
	if c.Addr != "" {
		set = append(set, "addr")
	}
	if c.Matching != nil {
		set = append(set, "matching")
	}
	return set
}

// Matcher represents a goss-style matcher that can be either a scalar value
// (decoded as an implicit equality match) or an operator map such as
// {equals: X}, {contains: [...]}, {matches: "regex"}, {lt: N}, {gt: N}.
//
// Scalar form decodes to Op="equals", Value=<scalar>.
// Map form decodes to Op=<first key>, Value=<its value>.
type Matcher struct {
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// UnmarshalYAML accepts either a scalar or a single-key map.
func (m *Matcher) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var v any
		if err := node.Decode(&v); err != nil {
			return fmt.Errorf("decoding matcher scalar: %w", err)
		}
		m.Op = "equals"
		m.Value = v
		return nil
	case yaml.SequenceNode:
		var v []any
		if err := node.Decode(&v); err != nil {
			return fmt.Errorf("decoding matcher sequence: %w", err)
		}
		m.Op = "equals"
		m.Value = v
		return nil
	case yaml.MappingNode:
		var raw map[string]any
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("decoding matcher map: %w", err)
		}
		if len(raw) != 1 {
			return fmt.Errorf("matcher map must have exactly one operator key, got %d", len(raw))
		}
		for k, v := range raw {
			m.Op = k
			m.Value = v
		}
		return nil
	default:
		return fmt.Errorf("matcher: unsupported YAML kind %d", node.Kind)
	}
}

// MarshalYAML emits the canonical operator-map form so round-tripping is stable.
func (m Matcher) MarshalYAML() (any, error) {
	if m.Op == "equals" {
		return m.Value, nil
	}
	return map[string]any{m.Op: m.Value}, nil
}

// UnmarshalJSON keeps the JSON read path symmetric with UnmarshalYAML: it
// accepts the canonical `{"op":"...","value":...}` form (what our own
// writeJSONLabel emits) AND a bare scalar (`"PONG"` → equals). Hand-crafted
// OCI labels that use the authoring shorthand now parse without the
// otherwise-cryptic "cannot unmarshal string into struct" error.
func (m *Matcher) UnmarshalJSON(data []byte) error {
	s := bytes.TrimSpace(data)
	if len(s) == 0 {
		return nil
	}
	if s[0] == '{' {
		// Canonical or operator-map form.
		var tmp struct {
			Op    string `json:"op"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(data, &tmp); err == nil && tmp.Op != "" {
			m.Op = tmp.Op
			m.Value = tmp.Value
			return nil
		}
		// Operator-map form: {"equals": X} / {"contains": [...]}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		if len(raw) != 1 {
			return fmt.Errorf("matcher map must have exactly one operator key, got %d", len(raw))
		}
		for k, v := range raw {
			m.Op = k
			m.Value = v
		}
		return nil
	}
	// Scalar / array: decode into the Value field, Op defaults to equals.
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	m.Op = "equals"
	m.Value = v
	return nil
}

// MatcherList lets users write scalar/single-map shorthand where a list of
// Matchers is expected. `stdout: PONG` and `stdout: [PONG]` decode identically.
type MatcherList []Matcher

// UnmarshalYAML accepts a sequence (normal list) OR a scalar/single-map
// (wrapped in a one-element list).
func (ml *MatcherList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		var list []Matcher
		if err := node.Decode(&list); err != nil {
			return err
		}
		*ml = list
		return nil
	}
	var m Matcher
	if err := m.UnmarshalYAML(node); err != nil {
		return err
	}
	*ml = []Matcher{m}
	return nil
}

// UnmarshalJSON mirrors UnmarshalYAML's scalar-shorthand behavior for the
// JSON read path (OCI labels, hand-crafted JSON fixtures). Accepts:
//   - JSON array  → []Matcher
//   - JSON scalar → [{equals: scalar}]
//   - JSON object → [{op/value OR operator-map}]
func (ml *MatcherList) UnmarshalJSON(data []byte) error {
	s := bytes.TrimSpace(data)
	if len(s) == 0 {
		*ml = nil
		return nil
	}
	if s[0] == '[' {
		var list []Matcher
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*ml = list
		return nil
	}
	var m Matcher
	if err := m.UnmarshalJSON(data); err != nil {
		return err
	}
	*ml = []Matcher{m}
	return nil
}

// LabelTestSet is the three-section structure embedded in the
// org.overthinkos.tests OCI label: layer-contributed checks, image-level
// checks, and deploy-default checks. See the design decision in the plan
// file for why deploy defaults travel with the image.
type LabelTestSet struct {
	Layer  []Check `json:"layer,omitempty"`
	Image  []Check `json:"image,omitempty"`
	Deploy []Check `json:"deploy,omitempty"`
}

// IsEmpty returns true if no section has any checks. Used by label emission
// to omit the label entirely when there are no tests to ship.
func (s *LabelTestSet) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.Layer) == 0 && len(s.Image) == 0 && len(s.Deploy) == 0
}

// ---------------------------------------------------------------------------
// Variable expansion (extended grammar shared with tasks)
//
// The existing taskVarRefPattern in ov/tasks.go matches ${NAME}. Tests need
// parameterized refs like ${HOST_PORT:6379} and ${VOLUME_PATH:workspace} to
// address deploy-time values. testVarRefPattern is the extended grammar;
// it is a superset of the task pattern so task refs continue to work here.
// ---------------------------------------------------------------------------

// testVarRefPattern matches ${NAME} and ${NAME:arg} references. Group 1 is
// the variable name; group 2 is the optional argument (empty when absent).
//
// Backward-compatible widening of taskVarRefPattern at ov/tasks.go.
var testVarRefPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)(?::([^}]+))?\}`)

// ExpandTestVars substitutes ${NAME} and ${NAME:arg} references using the
// supplied environment map.
//
// Keys in env for plain refs use just the name: env["HOME"] = "/home/user".
// Keys for parameterized refs combine name and argument with a colon:
// env["HOST_PORT:6379"] = "16379", env["VOLUME_PATH:workspace"] = "/var/lib/…".
//
// Returns the substituted string and a list of unresolved refs (in encounter
// order, deduplicated). The caller decides whether unresolved refs are an
// error (build-time validation) or a skip reason (runtime).
func ExpandTestVars(s string, env map[string]string) (string, []string) {
	seen := map[string]bool{}
	var missing []string
	out := testVarRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := testVarRefPattern.FindStringSubmatch(match)
		name, arg := sub[1], sub[2]
		key := name
		if arg != "" {
			key = name + ":" + arg
		}
		if v, ok := env[key]; ok {
			return v
		}
		if !seen[key] {
			seen[key] = true
			missing = append(missing, key)
		}
		return match // leave unresolved refs visible in output
	})
	return out, missing
}

// TestVarRefs returns the set of ${NAME[:arg]} references in s, as their
// fully-qualified keys (matching the env-map format used by ExpandTestVars).
// Used by the validator to catch typos at config time.
func TestVarRefs(s string) []string {
	matches := testVarRefPattern.FindAllStringSubmatch(s, -1)
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		key := m[1]
		if m[2] != "" {
			key = m[1] + ":" + m[2]
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}

// runtimeOnlyVarPrefixes lists variable name prefixes that are only resolvable
// against a running container. scope:"build" checks must not reference these.
var runtimeOnlyVarPrefixes = []string{
	"HOST_PORT",
	"VOLUME_PATH",
	"VOLUME_CONTAINER_PATH",
	"CONTAINER_IP",
	"CONTAINER_NAME",
	"INSTANCE",
	"ENV_",
}

// IsRuntimeOnlyVar reports whether the given variable key (as returned by
// TestVarRefs) refers to a runtime-only value. The check matches on name
// prefix because parameterized vars share a common prefix with their arg.
func IsRuntimeOnlyVar(key string) bool {
	name := key
	if i := strings.IndexByte(key, ':'); i >= 0 {
		name = key[:i]
	}
	for _, p := range runtimeOnlyVarPrefixes {
		if name == p || strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Field-walking helpers
// ---------------------------------------------------------------------------

// StringFields returns pointers to every string-typed attribute on a Check
// that may contain ${VAR} references. Used by the expander and validator to
// iterate over substitutable fields without reflecting over the entire struct.
//
// The slice is stable (not affected by which verb is set) so callers can
// safely iterate and mutate in place.
func (c *Check) StringFields() []*string {
	return []*string{
		&c.File, &c.Package, &c.Service, &c.Process, &c.Command,
		&c.HTTP, &c.DNS, &c.User, &c.Group, &c.Interface,
		&c.KernelParam, &c.Mount, &c.Addr,
		&c.ID, &c.Description, &c.Timeout,
		&c.Mode, &c.Owner, &c.GroupOf, &c.Filetype, &c.Sha256,
		&c.IP, &c.CAFile, &c.Method, &c.RequestBody,
		&c.Server, &c.Home, &c.Shell,
		&c.MountSource, &c.Filesystem,
	}
}

// ExpandVars rewrites every ${...} reference on this Check in place using
// the supplied environment map. Returns the combined list of unresolved refs
// encountered across all string fields.
func (c *Check) ExpandVars(env map[string]string) []string {
	seen := map[string]bool{}
	var missing []string
	for _, p := range c.StringFields() {
		if *p == "" {
			continue
		}
		replaced, unresolved := ExpandTestVars(*p, env)
		*p = replaced
		for _, k := range unresolved {
			if !seen[k] {
				seen[k] = true
				missing = append(missing, k)
			}
		}
	}
	sort.Strings(missing)
	return missing
}
