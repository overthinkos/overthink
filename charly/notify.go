package main

import (
	"fmt"
	"os"
)

// sendVenueNotification sends a desktop notification on the venue (container /
// VM / host). Best-effort: silently ignores all errors (no daemon, no dbus,
// headless target). It delegates to a PRESENT `charly eval dbus notify .` when the
// image bakes one, else falls back to gdbus (glib2). Being an automatic
// side-effect (deploy / record / tmux), it deliberately does NOT trigger the
// generic charly copy-in that the EXPLICIT `charly eval dbus notify`/`call` paths use —
// transferring the 27 MB binary into a container just for a best-effort popup is
// not worth it, and desktops carry gdbus anyway.
func sendVenueNotification(ex DeployExecutor, title, body string) {
	if venueHasTool(ex, "charly") {
		script := fmt.Sprintf("charly eval dbus notify . %s %s",
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
				`"charly" 0 "" %s %s "[]" "{}" -- -1`,
			shellQuote(title), shellQuote(body))
		_ = venueRunSilent(ex, gdbusCmd) // best-effort
		return
	}

	fmt.Fprintf(os.Stderr, "Warning: cannot send notification — neither 'charly' nor 'gdbus' found on target\n")
}
