package main

import (
	"errors"
	"fmt"
	"testing"

	dbus "github.com/godbus/dbus/v5"
)

// fakeSSOps is a configurable ssOps implementation for unit tests.
type fakeSSOps struct {
	aliasMap       map[string]dbus.ObjectPath
	aliasErr       map[string]error
	collectionList []dbus.ObjectPath
	collectionsErr error
	labels         map[dbus.ObjectPath]string
	healthErrs     map[dbus.ObjectPath]error
	unlockErrs     map[dbus.ObjectPath]error
	// items: collectionPath -> service -> username -> itemPath ("" = not found)
	items      map[dbus.ObjectPath]map[string]map[string]dbus.ObjectPath
	searchErrs map[dbus.ObjectPath]error
}

func newFakeSSOps() *fakeSSOps {
	return &fakeSSOps{
		aliasMap:   map[string]dbus.ObjectPath{},
		aliasErr:   map[string]error{},
		labels:     map[dbus.ObjectPath]string{},
		healthErrs: map[dbus.ObjectPath]error{},
		unlockErrs: map[dbus.ObjectPath]error{},
		items:      map[dbus.ObjectPath]map[string]map[string]dbus.ObjectPath{},
		searchErrs: map[dbus.ObjectPath]error{},
	}
}

func (f *fakeSSOps) readAlias(name string) (dbus.ObjectPath, error) {
	if err, ok := f.aliasErr[name]; ok {
		return "", err
	}
	return f.aliasMap[name], nil
}

func (f *fakeSSOps) collections() ([]dbus.ObjectPath, error) {
	if f.collectionsErr != nil {
		return nil, f.collectionsErr
	}
	out := make([]dbus.ObjectPath, len(f.collectionList))
	copy(out, f.collectionList)
	return out, nil
}

func (f *fakeSSOps) isCollectionHealthy(path dbus.ObjectPath) error {
	return f.healthErrs[path]
}

func (f *fakeSSOps) collectionLabel(path dbus.ObjectPath) string {
	return f.labels[path]
}

func (f *fakeSSOps) unlock(path dbus.ObjectPath) error {
	return f.unlockErrs[path]
}

func (f *fakeSSOps) searchItem(path dbus.ObjectPath, service, username string) (dbus.ObjectPath, error) {
	if err, ok := f.searchErrs[path]; ok && err != nil {
		return "", err
	}
	coll, ok := f.items[path]
	if !ok {
		return "", ErrSSNotFound
	}
	svc, ok := coll[service]
	if !ok {
		return "", ErrSSNotFound
	}
	item, ok := svc[username]
	if !ok || item == "" {
		return "", ErrSSNotFound
	}
	return item, nil
}

func (f *fakeSSOps) addItem(coll dbus.ObjectPath, service, username string, item dbus.ObjectPath) {
	if f.items[coll] == nil {
		f.items[coll] = map[string]map[string]dbus.ObjectPath{}
	}
	if f.items[coll][service] == nil {
		f.items[coll][service] = map[string]dbus.ObjectPath{}
	}
	f.items[coll][service][username] = item
}

// --- Test cases ---

// TestFindItem_DefaultAliasHealthy: default alias resolves to a healthy
// collection that contains the item. Expect immediate match via alias,
// no iteration.
func TestFindItem_DefaultAliasHealthy(t *testing.T) {
	f := newFakeSSOps()
	const defaultPath = dbus.ObjectPath("/org/freedesktop/secrets/collection/login")
	f.aliasMap["default"] = defaultPath
	f.collectionList = []dbus.ObjectPath{defaultPath}
	f.labels[defaultPath] = "Login"
	f.addItem(defaultPath, "ov/enc", "immich-ml", "/items/pw1")

	item, label, err := findItemAcrossCollections(f, "ov/enc", "immich-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/pw1" {
		t.Errorf("item = %s, want /items/pw1", item)
	}
	if label != "Login" {
		t.Errorf("label = %q, want Login", label)
	}
}

// TestFindItem_DefaultAliasBroken_FallbackToIteration: mirrors the real
// KeePassXC bug. Default alias points at a broken stub collection; the
// real credential is in a sibling collection. Expect the broken collection
// to be skipped and the item to be found in the healthy sibling.
func TestFindItem_DefaultAliasBroken_FallbackToIteration(t *testing.T) {
	f := newFakeSSOps()
	const stub = dbus.ObjectPath("/org/freedesktop/secrets/collection/atrawog")
	const real = dbus.ObjectPath("/org/freedesktop/secrets/collection/atrawog_deb4")
	f.aliasMap["default"] = stub
	f.collectionList = []dbus.ObjectPath{stub, real}
	f.labels[stub] = ""
	f.labels[real] = "hexaplant"
	f.healthErrs[stub] = errors.New("Input/output error") // broken stub
	f.addItem(real, "ov/enc", "immich-ml", "/items/real-pw")

	item, label, err := findItemAcrossCollections(f, "ov/enc", "immich-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/real-pw" {
		t.Errorf("item = %s, want /items/real-pw", item)
	}
	if label != "hexaplant" {
		t.Errorf("label = %q, want hexaplant", label)
	}
}

// TestFindItem_PreferLabel_SelectsByLabel: when the default alias is
// healthy but the caller asked for a specific label, both the alias AND the
// preferred-label collection get tried. The alias target is checked first
// (it's higher priority), then the label match. If the item is in the
// label collection, it's found.
func TestFindItem_PreferLabel_SelectsByLabel(t *testing.T) {
	f := newFakeSSOps()
	const aliasTarget = dbus.ObjectPath("/collections/default")
	const labelTarget = dbus.ObjectPath("/collections/hexaplant")
	f.aliasMap["default"] = aliasTarget
	f.collectionList = []dbus.ObjectPath{aliasTarget, labelTarget}
	f.labels[aliasTarget] = "Default"
	f.labels[labelTarget] = "hexaplant"
	// Item only in the label collection, not in the default.
	f.addItem(labelTarget, "ov/enc", "immich-ml", "/items/in-hexaplant")

	item, label, err := findItemAcrossCollections(f, "ov/enc", "immich-ml", "hexaplant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/in-hexaplant" {
		t.Errorf("item = %s, want /items/in-hexaplant", item)
	}
	if label != "hexaplant" {
		t.Errorf("label = %q, want hexaplant", label)
	}
}

// TestFindItem_AllCollectionsBroken_ReturnsAllBroken: every collection is
// unhealthy. Expect ErrSSAllBroken, not ErrSSNotFound.
func TestFindItem_AllCollectionsBroken_ReturnsAllBroken(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	const c2 = dbus.ObjectPath("/collections/c2")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1, c2}
	f.healthErrs[c1] = errors.New("I/O error")
	f.healthErrs[c2] = errors.New("I/O error")

	_, _, err := findItemAcrossCollections(f, "ov/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSAllBroken) {
		t.Errorf("err = %v, want ErrSSAllBroken", err)
	}
}

// TestFindItem_NotFoundAnywhere_ReturnsNotFound: collections are healthy
// but the item is not in any of them. Expect ErrSSNotFound.
func TestFindItem_NotFoundAnywhere_ReturnsNotFound(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	const c2 = dbus.ObjectPath("/collections/c2")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1, c2}
	f.labels[c1] = "Login"
	f.labels[c2] = "Work"

	_, _, err := findItemAcrossCollections(f, "ov/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSNotFound) {
		t.Errorf("err = %v, want ErrSSNotFound", err)
	}
}

// TestFindItem_SearchErrorCountsAsBroken: a collection is healthy but
// SearchItems fails with an I/O error mid-search. If every candidate errors
// that way, result is ErrSSAllBroken; if at least one succeeds with
// not-found, result is ErrSSNotFound.
func TestFindItem_SearchErrorCountsAsBroken(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	const c2 = dbus.ObjectPath("/collections/c2")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1, c2}
	f.labels[c1] = "Login"
	f.labels[c2] = "Work"
	f.searchErrs[c1] = fmt.Errorf("I/O error")
	// c2 is healthy but has no matching item

	_, _, err := findItemAcrossCollections(f, "ov/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSNotFound) {
		t.Errorf("err = %v, want ErrSSNotFound (at least one search succeeded)", err)
	}

	// Now make c2 also error
	f.searchErrs[c2] = fmt.Errorf("I/O error")
	_, _, err = findItemAcrossCollections(f, "ov/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSAllBroken) {
		t.Errorf("err = %v, want ErrSSAllBroken (every search errored)", err)
	}
}

// TestFindItem_UnlockFailureCountsAsBroken: a collection is healthy but
// Unlock fails (prompt required, not supported in non-interactive path).
// Should count as a search error for bookkeeping.
func TestFindItem_UnlockFailureCountsAsBroken(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	f.aliasMap["default"] = c1
	f.collectionList = []dbus.ObjectPath{c1}
	f.labels[c1] = "Login"
	f.unlockErrs[c1] = errors.New("prompt required")

	_, _, err := findItemAcrossCollections(f, "ov/enc", "immich-ml", "")
	if !errors.Is(err, ErrSSAllBroken) {
		t.Errorf("err = %v, want ErrSSAllBroken (unlock failed on only candidate)", err)
	}
}

// TestFindItem_DefaultAliasUnsetButIterationFinds: the default alias is
// not set (readAlias returns empty), iteration walks the collection list
// and finds the item. This exercises the case where `/aliases/default` is
// literally unset (not broken).
func TestFindItem_DefaultAliasUnsetButIterationFinds(t *testing.T) {
	f := newFakeSSOps()
	const c1 = dbus.ObjectPath("/collections/c1")
	f.aliasMap["default"] = "" // unset (readAlias returns empty path, no error)
	f.collectionList = []dbus.ObjectPath{c1}
	f.labels[c1] = "Only"
	f.addItem(c1, "ov/enc", "immich-ml", "/items/found")

	item, label, err := findItemAcrossCollections(f, "ov/enc", "immich-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/found" {
		t.Errorf("item = %s, want /items/found", item)
	}
	if label != "Only" {
		t.Errorf("label = %q, want Only", label)
	}
}

// TestFindItem_DefaultAliasDeduped: when the default alias AND the
// concrete collection path both appear in the candidate list, the concrete
// path should NOT be searched twice (if the alias already resolved to the
// same path). Note: our dedup key is the object path string, so an alias
// path like "/aliases/default" is distinct from a concrete path like
// "/collection/atrawog_deb4" even when they route to the same collection.
// The implementation resolves the alias to the concrete path via readAlias
// before adding to candidates, which fixes this.
func TestFindItem_DefaultAliasDeduped(t *testing.T) {
	f := newFakeSSOps()
	const real = dbus.ObjectPath("/collections/real")
	f.aliasMap["default"] = real // resolves to the concrete path
	f.collectionList = []dbus.ObjectPath{real}
	f.labels[real] = "Login"
	f.addItem(real, "ov/enc", "immich-ml", "/items/pw")

	// If iteration happened twice, unlock would be called twice. We track
	// that via a counter in a wrapper.
	var unlockCount int
	wrap := &countingOps{fakeSSOps: f, unlockCount: &unlockCount}

	item, _, err := findItemAcrossCollections(wrap, "ov/enc", "immich-ml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != "/items/pw" {
		t.Errorf("item = %s, want /items/pw", item)
	}
	if unlockCount != 1 {
		t.Errorf("unlockCount = %d, want 1 (candidate deduped)", unlockCount)
	}
}

type countingOps struct {
	*fakeSSOps
	unlockCount *int
}

func (o *countingOps) unlock(path dbus.ObjectPath) error {
	*o.unlockCount++
	return o.fakeSSOps.unlock(path)
}
