package main

import (
	dbus "github.com/godbus/dbus/v5"
)

// keyring_unlock_signal.go keeps the ONE Secret Service DBus helper the core's
// encrypted-volume unlock-waiter (enc.go waitForKeyringUnlock — C6, stays in core) needs,
// after the Secret Service store-CRUD client (ssClient) moved out to candy/plugin-secrets
// (the credential-store externalization). enc.go owns its own godbus session-bus
// subscription; it only needs to recognize a collection-unlocked PropertiesChanged signal.
// godbus is NOT shed from the core by this cutover — only github.com/zalando/go-keyring is.

// ssCollectionInterface is the Secret Service Collection D-Bus interface name. Duplicated
// (one const) from the plugin's secret_service.go so the in-core unlock-waiter's signal
// matcher does not depend on the moved Secret Service client.
const ssCollectionInterface = "org.freedesktop.Secret.Collection"

// isCollectionUnlockedSignal returns true when sig is a DBus PropertiesChanged signal
// indicating a Secret Service collection's Locked property transitioned to false (unlocked).
// Used by the event-driven keyring-wait loop (enc.go) to wake only on relevant unlock events.
func isCollectionUnlockedSignal(sig *dbus.Signal) bool {
	if sig == nil || sig.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" {
		return false
	}
	if len(sig.Body) < 2 {
		return false
	}
	iface, _ := sig.Body[0].(string)
	if iface != ssCollectionInterface {
		return false
	}
	changed, _ := sig.Body[1].(map[string]dbus.Variant)
	v, ok := changed["Locked"]
	if !ok {
		return false
	}
	locked, ok := v.Value().(bool)
	return ok && !locked
}
