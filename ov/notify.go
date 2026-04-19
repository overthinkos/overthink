package main

import (
	"fmt"
	"os"
	"os/exec"
)

// sendContainerNotification sends a desktop notification inside a container.
// Best-effort: silently ignores all errors (no daemon, no dbus, headless container).
func sendContainerNotification(engine, containerName, title, body string) {
	if engine == "" {
		// Local mode (inside container): connect to session bus directly
		dbusNotifyLocal(title, body) //nolint: errcheck — best-effort
		return
	}

	// Remote mode: delegate to container's ov binary, fall back to gdbus
	if checkToolAvailable(engine, containerName, "ov") == nil {
		cmd := exec.Command(engine, "exec", containerName, "ov", "test", "dbus", "notify", ".", title, body)
		if cmd.Run() == nil {
			return
		}
	}

	// Fallback: gdbus call inside container
	if checkToolAvailable(engine, containerName, "gdbus") == nil {
		gdbusCmd := fmt.Sprintf(
			`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && `+
				`gdbus call --session `+
				`--dest=org.freedesktop.Notifications `+
				`--object-path=/org/freedesktop/Notifications `+
				`--method=org.freedesktop.Notifications.Notify `+
				`"ov" 0 "" %s %s "[]" "{}" -- -1`,
			shellQuote(title), shellQuote(body))
		cmd := exec.Command(engine, "exec", containerName, "sh", "-c", gdbusCmd)
		cmd.Run() //nolint: errcheck — best-effort
		return
	}

	fmt.Fprintf(os.Stderr, "Warning: cannot send notification — neither 'ov' nor 'gdbus' found in container\n")
}
