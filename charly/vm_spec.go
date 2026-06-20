package main

// Note: per /charly-internals:disposable, disposability is a DEPLOY property and
// lives exclusively on BundleNode. `VmSpec.Disposable`
// / `VmSpec.Lifecycle` are not VmSpec fields — the disposability of a VM is
// read from the BundleNode(s) that reference it via `vm:`, through
// BundleNode.IsDisposable() (deploy.go). `charly update` does NOT gate on
// it (the verb obeys any explicit invocation); it only NOTES a non-disposable
// target for operator transparency (noteUpdateDisposability). `charly migrate`
// moves any residual flags on a user's on-disk configs to the
// matching deployment entries.
