package main

// install_ledger.go — persistent record of host deploys.
//
// Every `ov deploy add host …` writes structured records to a ledger
// so a later `ov deploy del host …` can reverse the exact operations.
// Layout:
//
//   ~/.config/overthink/installed/
//     .lock                          flock for concurrent sessions
//     deploys/
//       <deploy-id>.json             image + add_layers + layers list
//     layers/
//       <layer-name>.json            per-layer steps + deployed_by set
//
// Refcounting lives in the layer files: `deployed_by` is the set of
// deploy IDs that include this layer. Uninstalling one deploy
// decrements the set; only when it becomes empty does the layer's
// steps actually reverse.
//
// This file implements I/O (read/write/lock) and ledger-shape types.
// The actual reverse-execution logic lives in deploy_target_host.go.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LedgerPaths describes where ledger files live on disk. Extracted so
// tests can redirect to a temp dir.
type LedgerPaths struct {
	Root     string // ~/.config/overthink/installed
	Deploys  string // <Root>/deploys/
	Layers   string // <Root>/layers/
	LockFile string // <Root>/.lock
}

// DefaultLedgerPaths returns the canonical paths anchored at the
// invoking user's home directory.
func DefaultLedgerPaths() (*LedgerPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("DefaultLedgerPaths: %w", err)
	}
	root := filepath.Join(home, ".config", "overthink", "installed")
	return &LedgerPaths{
		Root:     root,
		Deploys:  filepath.Join(root, "deploys"),
		Layers:   filepath.Join(root, "layers"),
		LockFile: filepath.Join(root, ".lock"),
	}, nil
}

// Ensure creates the ledger directory tree if missing.
func (p *LedgerPaths) Ensure() error {
	for _, d := range []string{p.Root, p.Deploys, p.Layers} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("ledger mkdir %s: %w", d, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Flock — serialize concurrent ov deploy sessions.
// ---------------------------------------------------------------------------

// LedgerLock is an acquired advisory lock on the ledger directory. Call
// Release() when done. Panic-safe via defer.
type LedgerLock struct {
	f *os.File
}

// AcquireLedgerLock opens the lock file (creating if absent) and takes
// an exclusive flock. Blocks until the lock is available.
func AcquireLedgerLock(paths *LedgerPaths) (*LedgerLock, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(paths.LockFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("ledger lock open: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("ledger lock flock: %w", err)
	}
	return &LedgerLock{f: f}, nil
}

// Release releases the flock and closes the file.
func (l *LedgerLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
	l.f = nil
	return err
}

// ---------------------------------------------------------------------------
// Ledger records
// ---------------------------------------------------------------------------

// DeployRecord is the top-level entry in deploys/<deploy-id>.json.
// Lists the image, tag, and the ordered layer set included in this
// deploy (image layers + add_layers overlays, already topo-sorted).
type DeployRecord struct {
	DeployID   string   `json:"deploy_id"`
	Image      string   `json:"image"`
	Tag        string   `json:"tag,omitempty"`
	Target     string   `json:"target"` // "host" | "container:<name>"
	Layers     []string `json:"layers"`
	AddLayers  []string `json:"add_layers,omitempty"`
	DeployedAt string   `json:"deployed_at"`
}

// LayerRecord is the per-layer ledger entry. Lists concrete artifacts
// (packages installed, files written, services enabled, env.d file
// created, repo changes) so reversal doesn't need to re-compile the
// plan from layer.yml.
type LayerRecord struct {
	Layer         string        `json:"layer"`
	Version       string        `json:"version,omitempty"`
	DeployedBy    []string      `json:"deployed_by"`              // set of deploy IDs
	DeployedAt    string        `json:"deployed_at"`
	BuilderImage  string        `json:"builder_image,omitempty"`
	Steps         []StepRecord  `json:"steps,omitempty"`          // completed steps, in install order
	ReverseOps    []ReverseOp   `json:"reverse_ops,omitempty"`    // precomputed ops for teardown
}

// StepRecord is a thin summary of a completed InstallStep that the
// ledger keeps for audit. Kept intentionally small — the ReverseOps
// list on LayerRecord is the source of truth for teardown.
type StepRecord struct {
	Kind        StepKind          `json:"kind"`
	Scope       Scope             `json:"scope,omitempty"`
	Venue       Venue             `json:"venue,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	CompletedAt string            `json:"completed_at"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// ---------------------------------------------------------------------------
// I/O
// ---------------------------------------------------------------------------

// WriteDeployRecord serializes rec to deploys/<deploy-id>.json.
func WriteDeployRecord(paths *LedgerPaths, rec *DeployRecord) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	path := filepath.Join(paths.Deploys, rec.DeployID+".json")
	return writeJSONAtomic(path, rec)
}

// ReadDeployRecord loads deploys/<deploy-id>.json; returns nil, nil if
// the file doesn't exist.
func ReadDeployRecord(paths *LedgerPaths, id string) (*DeployRecord, error) {
	path := filepath.Join(paths.Deploys, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ReadDeployRecord: %w", err)
	}
	var rec DeployRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("ReadDeployRecord: parsing %s: %w", path, err)
	}
	return &rec, nil
}

// WriteLayerRecord serializes rec to layers/<layer>.json.
func WriteLayerRecord(paths *LedgerPaths, rec *LayerRecord) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	path := filepath.Join(paths.Layers, rec.Layer+".json")
	return writeJSONAtomic(path, rec)
}

// ReadLayerRecord loads layers/<layer>.json; returns nil, nil if absent.
func ReadLayerRecord(paths *LedgerPaths, layer string) (*LayerRecord, error) {
	path := filepath.Join(paths.Layers, layer+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ReadLayerRecord: %w", err)
	}
	var rec LayerRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("ReadLayerRecord: parsing %s: %w", path, err)
	}
	return &rec, nil
}

// DeleteDeployRecord removes deploys/<deploy-id>.json; silently ignores
// not-found (teardown is idempotent).
func DeleteDeployRecord(paths *LedgerPaths, id string) error {
	path := filepath.Join(paths.Deploys, id+".json")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeleteLayerRecord removes layers/<layer>.json.
func DeleteLayerRecord(paths *LedgerPaths, layer string) error {
	path := filepath.Join(paths.Layers, layer+".json")
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// writeJSONAtomic writes data to path via a temp file + rename so
// readers never see a partial write.
func writeJSONAtomic(path string, data interface{}) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Refcount helpers
// ---------------------------------------------------------------------------

// AddLayerDeployment adds deployID to layer.DeployedBy and writes the
// record. Used at install time.
func AddLayerDeployment(paths *LedgerPaths, layerName, deployID string, update func(*LayerRecord)) error {
	rec, err := ReadLayerRecord(paths, layerName)
	if err != nil {
		return err
	}
	if rec == nil {
		rec = &LayerRecord{
			Layer:      layerName,
			DeployedAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	if !containsString(rec.DeployedBy, deployID) {
		rec.DeployedBy = append(rec.DeployedBy, deployID)
	}
	if update != nil {
		update(rec)
	}
	return WriteLayerRecord(paths, rec)
}

// RemoveLayerDeployment decrements a layer's deployed_by set. Returns
// (recordAfter, shouldFullyRemove, error). When shouldFullyRemove is
// true, the caller should perform the actual file/package/service
// teardown and then delete the layer ledger entry.
func RemoveLayerDeployment(paths *LedgerPaths, layerName, deployID string) (*LayerRecord, bool, error) {
	rec, err := ReadLayerRecord(paths, layerName)
	if err != nil {
		return nil, false, err
	}
	if rec == nil {
		return nil, false, nil // already gone
	}
	out := rec.DeployedBy[:0]
	for _, id := range rec.DeployedBy {
		if id != deployID {
			out = append(out, id)
		}
	}
	rec.DeployedBy = out
	if len(rec.DeployedBy) == 0 {
		return rec, true, nil
	}
	return rec, false, WriteLayerRecord(paths, rec)
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
