package main

import (
	"fmt"
	"strings"
	"text/template"
)

// templateFuncs provides helper functions for format/builder templates.
var templateFuncs = template.FuncMap{
	// cacheMounts renders --mount=type=cache flags for BuildKit.
	"cacheMounts": func(mounts []CacheMountDef) string {
		if len(mounts) == 0 {
			return ""
		}
		var parts []string
		for _, m := range mounts {
			sharing := m.Sharing
			if sharing == "" {
				sharing = "locked"
			}
			parts = append(parts, fmt.Sprintf("--mount=type=cache,dst=%s,sharing=%s", m.Dst, sharing))
		}
		return strings.Join(parts, " \\\n    ")
	},

	// cacheMountsOwned renders cache mounts with uid/gid ownership.
	// Returns the mount flags with a trailing line continuation for chaining.
	"cacheMountsOwned": func(mounts []CacheMountDef, uid, gid int) string {
		if len(mounts) == 0 {
			return ""
		}
		var parts []string
		for _, m := range mounts {
			parts = append(parts, fmt.Sprintf("--mount=type=cache,dst=%s,uid=%d,gid=%d", m.Dst, uid, gid))
		}
		return strings.Join(parts, " \\\n    ") + " \\\n    "
	},

	// quote returns a shell-safe quoted string.
	"quote": func(s interface{}) string {
		return fmt.Sprintf("%q", fmt.Sprint(s))
	},

	// default returns the value if non-empty, otherwise the fallback.
	"default": func(val, fallback interface{}) interface{} {
		s := fmt.Sprint(val)
		if s == "" || s == "<nil>" {
			return fallback
		}
		return val
	},

	// splitFirst splits a string by sep and returns the first part.
	"splitFirst": func(s, sep string) string {
		parts := strings.SplitN(s, sep, 2)
		return parts[0]
	},

	// replace performs string replacement.
	"replace": func(s, old, new string) string {
		return strings.ReplaceAll(s, old, new)
	},

	// join joins a string slice with a separator.
	"join": func(elems interface{}, sep string) string {
		switch v := elems.(type) {
		case []string:
			return strings.Join(v, sep)
		case []interface{}:
			strs := make([]string, len(v))
			for i, e := range v {
				strs[i] = fmt.Sprint(e)
			}
			return strings.Join(strs, sep)
		default:
			return fmt.Sprint(elems)
		}
	},

	// printf is a template-accessible Sprintf.
	"printf": fmt.Sprintf,
}

// InstallContext provides data to format install templates.
type InstallContext struct {
	CacheMounts []CacheMountDef
	Packages    []string
	Repos       []map[string]interface{}
	Options     []string
	// Format-specific fields accessed via Raw
	Copr    []string
	Modules []string
	Exclude []string
	Keys    []string
	// Builder-specific
	StageName  string
	BuilderRef string
	User       string
	UID        int
	GID        int
	Home       string
}

// BuildStageContext provides data to builder stage templates.
type BuildStageContext struct {
	BuilderRef   string
	StageName    string
	LayerStage   string // scratch stage name for COPY --from
	CopySrc      string // build context path for layer files (e.g., "layers/python")
	UID          int
	GID          int
	Home         string
	User         string
	Manifest     string
	HasLockFile  bool
	InstallCmd   string
	ManylinuxFix string
	CacheMounts  []CacheMountDef
	Packages       []string // for config-detected builders (aur)
	Options        []string // for config-detected builders (aur)
	HasBuildScript bool     // true if layer has a build script (e.g., build.sh)
	BuildScript    string   // build script filename
}

// RenderTemplate renders a Go text/template with the given context.
func RenderTemplate(name, tmplStr string, ctx interface{}) (string, error) {
	if tmplStr == "" {
		return "", nil
	}
	tmpl, err := template.New(name).Funcs(templateFuncs).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("parsing template %q: %w", name, err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("executing template %q: %w", name, err)
	}
	return b.String(), nil
}

// NewInstallContext creates an InstallContext from a generic PackageSection.
func NewInstallContext(section map[string]interface{}, cacheMounts []CacheMountDef) *InstallContext {
	ctx := &InstallContext{
		CacheMounts: cacheMounts,
	}

	if pkgs, ok := section["packages"]; ok {
		ctx.Packages = toStringSlice(pkgs)
	}
	if repos, ok := section["repos"]; ok {
		ctx.Repos = toMapSlice(repos)
	}
	if opts, ok := section["options"]; ok {
		ctx.Options = toStringSlice(opts)
	}
	if copr, ok := section["copr"]; ok {
		ctx.Copr = toStringSlice(copr)
	}
	if mods, ok := section["modules"]; ok {
		ctx.Modules = toStringSlice(mods)
	}
	if excl, ok := section["exclude"]; ok {
		ctx.Exclude = toStringSlice(excl)
	}
	if keys, ok := section["keys"]; ok {
		ctx.Keys = toStringSlice(keys)
	}

	return ctx
}

// toStringSlice converts an interface{} to []string.
func toStringSlice(v interface{}) []string {
	switch val := v.(type) {
	case []string:
		return val
	case []interface{}:
		result := make([]string, len(val))
		for i, e := range val {
			result[i] = fmt.Sprint(e)
		}
		return result
	default:
		return nil
	}
}

// toMapSlice converts an interface{} to []map[string]interface{}.
func toMapSlice(v interface{}) []map[string]interface{} {
	switch val := v.(type) {
	case []interface{}:
		result := make([]map[string]interface{}, 0, len(val))
		for _, e := range val {
			if m, ok := e.(map[string]interface{}); ok {
				result = append(result, m)
			}
		}
		return result
	default:
		return nil
	}
}
