package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// labelStringRenames maps each pre-2026-06 PLURAL `org.overthinkos.<seg>`
// OCI-label segment to its singular form. These strings appear in a config
// ONLY where it references a baked label by NAME — never as an authoring key
// (those are singularized by the field-singular table). The in-repo case is
// build.yml's init `label_key: org.overthinkos.service.<init>`; the broader
// set covers a forked layer.yml `oci_label:` override or an eval `command:`
// that inspects a label. The `org.overthinkos.` prefix anchors every rename,
// so a bare authoring key is never touched. Order-independent + idempotent:
// each plural carries a trailing `s` (or `_`/`.` boundary) so no singular form
// is ever a match target on a re-run.
var labelStringRenames = [][2]string{
	{"org.overthinkos.services", "org.overthinkos.service"},
	{"org.overthinkos.ports", "org.overthinkos.port"},
	{"org.overthinkos.volumes", "org.overthinkos.volume"},
	{"org.overthinkos.aliases", "org.overthinkos.alias"},
	{"org.overthinkos.hooks", "org.overthinkos.hook"},
	{"org.overthinkos.routes", "org.overthinkos.route"},
	{"org.overthinkos.secrets", "org.overthinkos.secret"},
	{"org.overthinkos.skills", "org.overthinkos.skill"},
	{"org.overthinkos.env_layers", "org.overthinkos.env_layer"},
	{"org.overthinkos.port_protos", "org.overthinkos.port_proto"},
	{"org.overthinkos.layer_versions", "org.overthinkos.layer_version"},
	{"org.overthinkos.platform.formats", "org.overthinkos.platform.format"},
	{"org.overthinkos.builder.uses", "org.overthinkos.builder.use"},
	{"org.overthinkos.builder.provides", "org.overthinkos.builder.provide"},
	{"org.overthinkos.env_provides", "org.overthinkos.env_provide"},
	{"org.overthinkos.env_requires", "org.overthinkos.env_require"},
	{"org.overthinkos.env_accepts", "org.overthinkos.env_accept"},
	{"org.overthinkos.secret_accepts", "org.overthinkos.secret_accept"},
	{"org.overthinkos.secret_requires", "org.overthinkos.secret_require"},
	{"org.overthinkos.mcp_provides", "org.overthinkos.mcp_provide"},
	{"org.overthinkos.mcp_requires", "org.overthinkos.mcp_require"},
	{"org.overthinkos.mcp_accepts", "org.overthinkos.mcp_accept"},
}

// rewriteSingularLabel applies every labelStringRenames pair to raw bytes.
// Returns the rewritten bytes and whether anything changed.
func rewriteSingularLabel(data []byte) ([]byte, bool) {
	s := string(data)
	out := s
	for _, p := range labelStringRenames {
		out = strings.ReplaceAll(out, p[0], p[1])
	}
	return []byte(out), out != s
}

// MigrateSingularLabel walks the project rooted at dir and rewrites every
// PLURAL `org.overthinkos.*` label-string reference to its singular form
// (the 2026-06 singular-label cutover). Idempotent: a second invocation on
// the same tree returns nil. The baked OCI labels inside built images cannot
// be migrated by config rewriting — they are re-emitted singular on the next
// `ov box build` (a hard-cutover rebuild).
func MigrateSingularLabel(dir string, dryRun bool) ([]string, error) {
	files, err := discoverProjectYAMLs(dir)
	if err != nil {
		return nil, err
	}
	suffix := fmt.Sprintf(".bak.%d", time.Now().Unix())
	var rewritten []string
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		newData, changed := rewriteSingularLabel(data)
		if !changed {
			continue
		}
		// Idempotency invariant: after one pass, no plural label string remains.
		if _, residual := rewriteSingularLabel(newData); residual {
			return rewritten, fmt.Errorf("%s: post-rewrite residual plural label string — migrator bug", path)
		}
		if !dryRun {
			backupPath := path + suffix
			if _, err := os.Stat(backupPath); err == nil {
				return rewritten, fmt.Errorf("%s: backup already exists; refusing to overwrite", backupPath)
			}
			if err := os.WriteFile(backupPath, data, 0644); err != nil {
				return rewritten, fmt.Errorf("writing backup %s: %w", backupPath, err)
			}
			if err := os.WriteFile(path, newData, 0644); err != nil {
				return rewritten, fmt.Errorf("writing %s: %w", path, err)
			}
		}
		rewritten = append(rewritten, path)
	}
	return rewritten, nil
}
