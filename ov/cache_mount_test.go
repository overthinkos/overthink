package main

import (
	"strings"
	"testing"
)

// TestSharedCacheMount_StableID locks in the format that makes BuildKit
// caches survive layer-hash churn — the entire reason CacheMount exists.
func TestSharedCacheMount_StableID(t *testing.T) {
	got := SharedCacheMount("/var/cache/libdnf5", "").String()
	want := "--mount=type=cache,id=ov-var-cache-libdnf5,dst=/var/cache/libdnf5,sharing=locked"
	if got != want {
		t.Errorf("SharedCacheMount default sharing\n  got:  %s\n  want: %s", got, want)
	}

	got = SharedCacheMount("/var/cache/pacman/pkg", "shared").String()
	want = "--mount=type=cache,id=ov-var-cache-pacman-pkg,dst=/var/cache/pacman/pkg,sharing=shared"
	if got != want {
		t.Errorf("SharedCacheMount nested path\n  got:  %s\n  want: %s", got, want)
	}
}

// TestOwnedCacheMount_UIDInID confirms uid is part of the id namespace so
// different-uid builds don't collide on file ownership inside the cache volume.
func TestOwnedCacheMount_UIDInID(t *testing.T) {
	got := OwnedCacheMount("/tmp/pixi-cache", 1000, 1000).String()
	want := "--mount=type=cache,id=ov-tmp-pixi-cache-uid1000,dst=/tmp/pixi-cache,uid=1000,gid=1000"
	if got != want {
		t.Errorf("OwnedCacheMount\n  got:  %s\n  want: %s", got, want)
	}

	// Same dst, different uid → different id (the whole point).
	a := OwnedCacheMount("/tmp/npm-cache", 1000, 1000).String()
	b := OwnedCacheMount("/tmp/npm-cache", 2000, 2000).String()
	if a == b {
		t.Errorf("uid must differentiate the cache id; both produced:\n  %s", a)
	}
	if !strings.Contains(a, "uid1000") || !strings.Contains(b, "uid2000") {
		t.Errorf("expected uid suffix in id; got\n  a=%s\n  b=%s", a, b)
	}
}

// TestRenderCacheMounts_Empty must NOT emit a trailing separator when the
// slice is empty — otherwise generated Containerfiles get a stray `\` line.
func TestRenderCacheMounts_Empty(t *testing.T) {
	if got := RenderCacheMounts(nil, -1, 0, " \\\n    ", true); got != "" {
		t.Errorf("empty mounts must produce empty string, got: %q", got)
	}
}

// TestRenderCacheMounts_TrailingSeparator covers the cacheMountsOwned shape
// where we need the separator after the last entry (template chains into RUN body).
func TestRenderCacheMounts_TrailingSeparator(t *testing.T) {
	mounts := []CacheMountDef{{Dst: "/tmp/pixi-cache"}}
	got := RenderCacheMounts(mounts, 1000, 1000, " \\\n    ", true)
	if !strings.HasSuffix(got, " \\\n    ") {
		t.Errorf("trailing separator missing; got: %q", got)
	}
	if !strings.Contains(got, "id=ov-tmp-pixi-cache-uid1000") {
		t.Errorf("expected stable id; got: %q", got)
	}
}

// TestCacheMountID_StableAcrossInvocations is the core regression guard:
// the same dst MUST produce the same id every time, otherwise cache is
// keyed by something volatile and breaks the entire purpose of the fix.
func TestCacheMountID_StableAcrossInvocations(t *testing.T) {
	for i := 0; i < 10; i++ {
		a := SharedCacheMount("/var/cache/libdnf5", "locked").String()
		b := SharedCacheMount("/var/cache/libdnf5", "locked").String()
		if a != b {
			t.Fatalf("non-deterministic id at iteration %d:\n  a=%s\n  b=%s", i, a, b)
		}
	}
}
