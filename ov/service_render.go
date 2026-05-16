package main

// service_render.go — generic service schema + per-init-system renderer.
//
// Today's layer.yml has two separate service fields:
//   - service: (raw supervisord INI fragment — container-only)
//   - system_services: (list of systemd unit names to enable)
//
// Both collapse into one unified `services:` schema keyed by service
// name. Each entry is either `use_packaged: <unit>` (reuse a distro-
// shipped systemd unit with optional drop-in overrides) or a fully
// structured spec (`exec`, `env`, `restart`, `after`, `before`,
// `scope`, …) that gets rendered by the init-system's service_template
// in build.yml.
//
// This file declares the schema types, the rendering context, and the
// template rendering helpers. It does NOT parse layer.yml — that
// happens in layers.go alongside the rest of the LayerYAML unmarshaller.

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"
)

// ServiceEntry is one entry from layer.yml `services:` list.
//
// Authoring examples:
//
//	services:
//	  # Reuse packaged systemd unit.
//	  - name: postgresql
//	    use_packaged: postgresql.service
//	    enable: true
//	    scope: system
//	    overrides:
//	      env:
//	        PGDATA: /var/lib/postgresql/data
//
//	  # Custom service.
//	  - name: ollama
//	    exec: /usr/bin/ollama serve
//	    env:
//	      OLLAMA_HOST: "0.0.0.0:11434"
//	    restart: always
//	    scope: system
//	    enable: true
type ServiceEntry struct {
	// Identity
	Name string `yaml:"name"`

	// Packaged-unit reuse path — when non-empty, this entry enables an
	// existing systemd unit shipped by a distro package. The Exec/Env/
	// Restart/etc. fields are ignored; Overrides get rendered as a
	// drop-in instead.
	UsePackaged string `yaml:"use_packaged,omitempty"`

	// Custom-unit path — mutually exclusive with UsePackaged. The
	// init-system's service_template (in build.yml) renders these into
	// the native unit format (supervisord INI or systemd unit file).
	Exec             string            `yaml:"exec,omitempty"`
	Env              map[string]string `yaml:"env,omitempty"`
	Restart          string            `yaml:"restart,omitempty"` // no | on-failure | always | unless-stopped
	WorkingDirectory string            `yaml:"working_directory,omitempty"`
	User             string            `yaml:"user,omitempty"`
	After            []string          `yaml:"after,omitempty"`
	Before           []string          `yaml:"before,omitempty"`
	Stdout           string            `yaml:"stdout,omitempty"` // journal | file:<path> | none
	StopTimeout      string            `yaml:"stop_timeout,omitempty"`

	// Common to both paths
	Scope     string            `yaml:"scope,omitempty"` // system | user; default system
	Enable    bool              `yaml:"enable,omitempty"`
	Overrides *ServiceOverrides `yaml:"overrides,omitempty"`

	// --- supervisord/systemd lifecycle directives ---

	// Kind discriminates supervisord [program:X] vs [eventlistener:X].
	// Default "" == "program". systemd ignores this (renders as Service unit).
	Kind string `yaml:"kind,omitempty"`

	// Events — supervisord eventlistener trigger list (comma-separated),
	// e.g. "PROCESS_STATE_FATAL". Required when kind: eventlistener.
	Events string `yaml:"events,omitempty"`

	// AutoStart is tri-state: nil → template default (true for program,
	// true for eventlistener); *false → autostart=false; *true → autostart=true.
	// Use `auto_start: false` for services started manually (e.g., chrome
	// started by the compositor once Wayland is up).
	AutoStart *bool `yaml:"auto_start,omitempty"`

	// StartRetries — max (re)start attempts before the program enters FATAL.
	// Supervisord default is 3. Set explicitly to override.
	StartRetries int `yaml:"start_retries,omitempty"`

	// StartSecs — seconds the process must stay up to count as "started."
	// Default 1; longer values are needed for services like Wayland
	// compositors that take time to reach steady state.
	StartSecs int `yaml:"start_secs,omitempty"`

	// StopSignal — signal sent on graceful stop. Default TERM; some
	// programs need INT or HUP. Case-insensitive.
	StopSignal string `yaml:"stop_signal,omitempty"`

	// ExitCodes — comma-separated "successful" exit codes. Supervisord
	// default is "0,2". Relevant only when Restart == "no" or "on-failure".
	ExitCode string `yaml:"exit_code,omitempty"`

	// Priority — startup / shutdown order. Lower = earlier to start,
	// later to stop. Supervisord default 999.
	Priority int `yaml:"priority,omitempty"`
}

// ServiceOverrides describes drop-in modifications to a packaged unit.
// Currently a subset of the custom-unit fields; any field left empty
// means "preserve the packaged unit's value." Future fields (WantedBy,
// RequiredBy, …) can be added here without schema changes elsewhere.
type ServiceOverrides struct {
	Env   map[string]string `yaml:"env,omitempty"`
	After []string          `yaml:"after,omitempty"`
	Exec  string            `yaml:"exec,omitempty"` // override ExecStart
}

// IsPackaged returns true for entries that reuse a distro-shipped unit.
func (e *ServiceEntry) IsPackaged() bool {
	return e != nil && e.UsePackaged != ""
}

// EffectiveScope returns the entry's scope, defaulting to "system".
func (e *ServiceEntry) EffectiveScope() string {
	if e == nil || e.Scope == "" {
		return "system"
	}
	return e.Scope
}

// ---------------------------------------------------------------------------
// ServiceRenderContext — data passed to init-system service_template.
// ---------------------------------------------------------------------------

// ServiceRenderContext is the template-rendering context exposed to
// build.yml's init.<name>.service_template and its siblings. Keeps the
// interface surface tight: anything the renderer needs is a field here;
// nothing else is reachable.
type ServiceRenderContext struct {
	Name             string
	Layer            string
	Exec             string
	Env              map[string]string
	EnvList          []KeyValue // ordered env for deterministic template iteration
	Restart          string
	WorkingDirectory string
	User             string
	After            []string
	Before           []string
	Stdout           string
	StopTimeout      string
	Scope            string // "system" | "user"
	PackagedUnit     string // non-empty for drop-in rendering
	Home             string // invoking user's home — for user-scope unit paths
	SystemUnitDir    string // e.g. "/etc/systemd/system"
	UserUnitDir      string // e.g. "/home/user/.config/systemd/user"
	FragmentDir      string // supervisord fragment directory

	// Lifecycle directives (supervisord + systemd). See ServiceEntry for semantics.
	Kind         string
	Events       string
	AutoStart    *bool
	StartRetries int
	StartSecs    int
	StopSignal   string
	ExitCodes    string
	Priority     int
}

// KeyValue is a deterministic env-var ordering helper.
type KeyValue struct {
	Key   string
	Value string
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// RenderedService is the output of the renderer: the unit text, the
// path it should land at, and any drop-in path for packaged reuse.
type RenderedService struct {
	UnitText   string // "" for packaged-only entries (no body, just enable)
	UnitPath   string // where to write UnitText
	DropinText string // "" when no Overrides
	DropinPath string // drop-in file path when DropinText is non-empty
}

// RenderService turns a ServiceEntry into a RenderedService using the
// given init system's templates. Returns an error when the chosen init
// system has no template for the entry's shape (custom unit on
// supervisord-only init, packaged unit on supervisord, etc.).
func RenderService(entry *ServiceEntry, initDef *InitDef, ctx ServiceRenderContext) (*RenderedService, error) {
	if entry == nil {
		return nil, fmt.Errorf("RenderService: nil entry")
	}
	if initDef == nil || initDef.ServiceSchema == nil {
		return nil, fmt.Errorf("RenderService: init system has no service_schema")
	}
	schema := initDef.ServiceSchema
	out := &RenderedService{}
	ctx.Name = entry.Name
	// ctx.Layer is preserved from the caller (not overwritten here).
	ctx.Scope = entry.EffectiveScope()
	ctx.PackagedUnit = entry.UsePackaged
	ctx.Env = flattenedEnvMap(entry.Env, entry.Overrides)
	ctx.EnvList = sortedEnvList(ctx.Env)
	if entry.Exec != "" {
		ctx.Exec = entry.Exec
	}
	if entry.Overrides != nil && entry.Overrides.Exec != "" {
		ctx.Exec = entry.Overrides.Exec
	}
	if entry.WorkingDirectory != "" {
		ctx.WorkingDirectory = entry.WorkingDirectory
	}
	if entry.User != "" {
		ctx.User = entry.User
	}
	ctx.After = append(ctx.After, entry.After...)
	if entry.Overrides != nil {
		ctx.After = append(ctx.After, entry.Overrides.After...)
	}
	ctx.Before = append(ctx.Before, entry.Before...)
	ctx.Restart = entry.Restart
	ctx.Stdout = entry.Stdout
	ctx.StopTimeout = entry.StopTimeout
	// Lifecycle directives — passed through verbatim to the init-system template.
	ctx.Kind = entry.Kind
	ctx.Events = entry.Events
	ctx.AutoStart = entry.AutoStart
	ctx.StartRetries = entry.StartRetries
	ctx.StartSecs = entry.StartSecs
	ctx.StopSignal = entry.StopSignal
	ctx.ExitCodes = entry.ExitCode
	ctx.Priority = entry.Priority

	if entry.IsPackaged() {
		if !schema.SupportsPackaged {
			return nil, fmt.Errorf("init system %q does not support use_packaged (entry %s)", initDef.ManagementTool, entry.Name)
		}
		// Only the drop-in branch renders — no new unit body.
		if entry.Overrides != nil {
			text, err := renderTemplateString("service-dropin", schema.DropinTemplate, ctx)
			if err != nil {
				return nil, fmt.Errorf("rendering dropin for %s: %w", entry.Name, err)
			}
			path, err := renderTemplateString("dropin-path", schema.DropinPathTemplate, ctx)
			if err != nil {
				return nil, fmt.Errorf("rendering dropin path for %s: %w", entry.Name, err)
			}
			out.DropinText = text
			out.DropinPath = strings.TrimSpace(path)
		}
		return out, nil
	}

	// Custom unit path
	if schema.ServiceTemplate == "" {
		return nil, fmt.Errorf("init system %q has no service_template for custom entries", initDef.ManagementTool)
	}
	text, err := renderTemplateString("service-unit", schema.ServiceTemplate, ctx)
	if err != nil {
		return nil, fmt.Errorf("rendering unit for %s: %w", entry.Name, err)
	}
	path, err := renderTemplateString("service-path", schema.UnitPathTemplate, ctx)
	if err != nil {
		return nil, fmt.Errorf("rendering unit path for %s: %w", entry.Name, err)
	}
	out.UnitText = text
	out.UnitPath = strings.TrimSpace(path)
	return out, nil
}

// sortedEnvList returns a sorted-by-key slice of env entries.
// Deterministic ordering matters for template rendering — tests compare
// rendered output directly against golden strings.
func sortedEnvList(env map[string]string) []KeyValue {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]KeyValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, KeyValue{Key: k, Value: env[k]})
	}
	return out
}

// flattenedEnvMap composes base + overrides into one map, with
// overrides winning on conflict. Returns a fresh map; callers don't
// mutate base.
func flattenedEnvMap(base map[string]string, overrides *ServiceOverrides) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	if overrides != nil {
		for k, v := range overrides.Env {
			out[k] = v
		}
	}
	return out
}

// renderTemplateString executes a Go text/template with the standard
// helper funcs (join, supervisordRestart, systemdRestart, systemdStdout)
// plus whatever the caller's context provides.
func renderTemplateString(name, tmpl string, data interface{}) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	t, err := template.New(name).Funcs(serviceRenderFuncs()).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// serviceRenderFuncs returns the per-init-system helper functions
// referenced in build.yml templates. Adding a new init system means
// adding its helpers here so its templates stay readable.
func serviceRenderFuncs() template.FuncMap {
	return template.FuncMap{
		"join": func(s []string, sep string) string {
			return strings.Join(s, sep)
		},
		// derefBool dereferences a *bool for template conditionals —
		// callers check `{{if .AutoStart}}` for "explicitly set" then
		// `{{derefBool .AutoStart}}` to get the true/false value.
		"derefBool": func(b *bool) bool {
			if b == nil {
				return false
			}
			return *b
		},
		// systemdRestart maps the abstract `restart:` keyword to
		// systemd's Restart= policy.
		"systemdRestart": func(r string) string {
			switch r {
			case "always":
				return "always"
			case "on-failure":
				return "on-failure"
			case "unless-stopped":
				// systemd has no direct equivalent; `always` is the
				// closest semantic match (restart even on clean exit).
				return "always"
			case "no", "":
				return "no"
			}
			return "no"
		},
		// supervisordRestart maps `restart:` to supervisord's
		// autorestart= value.
		"supervisordRestart": func(r string) string {
			switch r {
			case "always":
				return "true"
			case "on-failure":
				return "unexpected"
			case "unless-stopped":
				return "true"
			case "no", "":
				return "false"
			}
			return "false"
		},
		// systemdStdout maps `stdout:` to systemd StandardOutput=.
		// "file:/path" renders as append:/path; everything else maps
		// directly.
		"systemdStdout": func(s string) string {
			if strings.HasPrefix(s, "file:") {
				return "append:" + strings.TrimPrefix(s, "file:")
			}
			if s == "" {
				return "journal"
			}
			return s
		},
		// supervisordLog handles supervisord's stdout_logfile= mapping.
		"supervisordLog": func(s string) string {
			if strings.HasPrefix(s, "file:") {
				return strings.TrimPrefix(s, "file:")
			}
			if s == "journal" || s == "" {
				return "/dev/stdout"
			}
			return s
		},
	}
}
