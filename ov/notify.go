package main

import (
	"fmt"
	"os"
)

// sendVenueNotification sends a desktop notification on the venue (container /
// VM / host). Best-effort: silently ignores all errors (no daemon, no dbus,
// headless target). Delegates to the venue's own `ov eval dbus notify .` so the
// notification reaches the live session bus inside the target, falling back to
// gdbus — the same pattern as dbusNotifyRemoteStrict, now venue-agnostic (R3).
func sendVenueNotification(ex DeployExecutor, title, body string) {
	if venueHasTool(ex, "ov") {
		script := fmt.Sprintf("ov eval dbus notify . %s %s",
			deployShellQuote(title), deployShellQuote(body))
		if venueRunSilent(ex, script) == nil {
			return
		}
	}

	// Fallback: gdbus call on the venue.
	if venueHasTool(ex, "gdbus") {
		gdbusCmd := fmt.Sprintf(
			`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && `+
				`gdbus call --session `+
				`--dest=org.freedesktop.Notifications `+
				`--object-path=/org/freedesktop/Notifications `+
				`--method=org.freedesktop.Notifications.Notify `+
				`"ov" 0 "" %s %s "[]" "{}" -- -1`,
			shellQuote(title), shellQuote(body))
		_ = venueRunSilent(ex, gdbusCmd) // best-effort
		return
	}

	fmt.Fprintf(os.Stderr, "Warning: cannot send notification — neither 'ov' nor 'gdbus' found on target\n")
}
