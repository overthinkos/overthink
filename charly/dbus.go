package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"
)

// DbusCmd interacts with D-Bus services inside containers.
type DbusCmd struct {
	Call       DbusCallCmd       `cmd:"" help:"Call a D-Bus method"`
	Introspect DbusIntrospectCmd `cmd:"" help:"Introspect a D-Bus service object"`
	List       DbusListCmd       `cmd:"" help:"List available D-Bus services"`
	Notify     DbusNotifyCmd     `cmd:"" help:"Send a desktop notification"`
}

// DbusNotifyCmd sends a desktop notification via org.freedesktop.Notifications.
type DbusNotifyCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Title    string `arg:"" help:"Notification title"`
	Body     string `arg:"" optional:"" default:"" help:"Notification body"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *DbusNotifyCmd) Run() error {
	if c.Image == "." {
		return dbusNotifyLocal(c.Title, c.Body)
	}
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return dbusNotifyRemoteStrict(venue.Exec, c.Title, c.Body)
}

// DbusCallCmd makes a generic D-Bus method call.
type DbusCallCmd struct {
	Image    string   `arg:"" help:"Image name (use . for local)"`
	Dest     string   `arg:"" help:"D-Bus service name (e.g. org.freedesktop.Notifications)"`
	Path     string   `arg:"" help:"Object path (e.g. /org/freedesktop/Notifications)"`
	Method   string   `arg:"" help:"Interface.Method (e.g. org.freedesktop.Notifications.Notify)"`
	Args     []string `arg:"" optional:"" help:"Method arguments (type:value, e.g. string:hello uint32:0)"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
}

func (c *DbusCallCmd) Run() error {
	if c.Image == "." {
		return dbusCallLocal(c.Dest, c.Path, c.Method, c.Args)
	}
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return dbusCallRemote(venue.Exec, c.Dest, c.Path, c.Method, c.Args)
}

// DbusListCmd lists available D-Bus services.
type DbusListCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *DbusListCmd) Run() error {
	if c.Image == "." {
		return dbusListLocal()
	}
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return dbusCallRemote(venue.Exec, "org.freedesktop.DBus", "/org/freedesktop/DBus",
		"org.freedesktop.DBus.ListNames", nil)
}

// DbusIntrospectCmd introspects a D-Bus service object.
type DbusIntrospectCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Dest     string `arg:"" help:"D-Bus service name"`
	Path     string `arg:"" help:"Object path"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *DbusIntrospectCmd) Run() error {
	if c.Image == "." {
		return dbusIntrospectLocal(c.Dest, c.Path)
	}
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return dbusCallRemote(venue.Exec, c.Dest, c.Path,
		"org.freedesktop.DBus.Introspectable.Introspect", nil)
}

// --- Local D-Bus operations (run inside container via godbus/dbus/v5) ---

func dbusConnectSession() (*dbus.Conn, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, dbusNoBusError()
	}
	return conn, nil
}

func dbusNotifyLocal(title, body string) error {
	conn, err := dbusConnectSession()
	if err != nil {
		return err
	}
	defer conn.Close()

	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		"charly",                  // app_name
		uint32(0),                 // replaces_id
		"",                        // app_icon
		title,                     // summary
		body,                      // body
		[]string{},                // actions
		map[string]dbus.Variant{}, // hints
		int32(-1),                 // expire_timeout (-1 = default)
	)
	return call.Err
}

func dbusCallLocal(dest, path, method string, args []string) error {
	conn, err := dbusConnectSession()
	if err != nil {
		return err
	}
	defer conn.Close()

	parsed, err := parseDbusArgs(args)
	if err != nil {
		return err
	}

	obj := conn.Object(dest, dbus.ObjectPath(path))
	call := obj.Call(method, 0, parsed...)
	if call.Err != nil {
		return call.Err
	}
	// Print return values
	for _, v := range call.Body {
		fmt.Println(v)
	}
	return nil
}

func dbusListLocal() error {
	conn, err := dbusConnectSession()
	if err != nil {
		return err
	}
	defer conn.Close()

	var names []string
	err = conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names)
	if err != nil {
		return err
	}
	for _, name := range names {
		fmt.Println(name)
	}
	return nil
}

func dbusIntrospectLocal(dest, path string) error {
	conn, err := dbusConnectSession()
	if err != nil {
		return err
	}
	defer conn.Close()

	var xml string
	obj := conn.Object(dest, dbus.ObjectPath(path))
	err = obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml)
	if err != nil {
		return err
	}
	fmt.Println(xml)
	return nil
}

// --- Remote D-Bus operations (host → container delegation) ---

// dbusNotifyRemoteStrict sends a notification to the venue (container / VM /
// host), returning errors (for charly eval dbus notify). Delegates to the venue's
// own `charly eval dbus notify .` so the call runs with the live session bus inside
// the target — the same delegation pattern, now venue-agnostic (R3).
func dbusNotifyRemoteStrict(ex DeployExecutor, title, body string) error {
	// Ensure an invokable charly on the venue — copying the host binary in when the
	// image doesn't bake the charly candy (the generic copy-in mechanism), then
	// delegate to it so the notify runs against the venue's own live session bus.
	if charlyCmd, err := EnsureCharlyInVenue(context.Background(), ex, EmitOpts{}); err == nil && charlyCmd != "" {
		script := fmt.Sprintf("%s eval dbus notify . %s %s", charlyCmd,
			deployShellQuote(title), deployShellQuote(body))
		if rerr := venueRun(ex, script); rerr == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "Warning: in-venue charly eval dbus failed, trying gdbus fallback\n")
	}

	// Fallback: gdbus call on the venue.
	if !venueHasTool(ex, "gdbus") {
		return dbusNoToolError(ex.Venue())
	}
	gdbusCmd := fmt.Sprintf(
		`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && `+
			`gdbus call --session `+
			`--dest=org.freedesktop.Notifications `+
			`--object-path=/org/freedesktop/Notifications `+
			`--method=org.freedesktop.Notifications.Notify `+
			`"charly" 0 "" %s %s "[]" "{}" -- -1`,
		shellQuote(title), shellQuote(body))
	return venueRun(ex, gdbusCmd)
}

// dbusCallRemote delegates a D-Bus call to the venue's charly binary, copying the
// host charly in when the image doesn't bake the charly candy (the generic copy-in).
func dbusCallRemote(ex DeployExecutor, dest, path, method string, args []string) error {
	charlyCmd, err := EnsureCharlyInVenue(context.Background(), ex, EmitOpts{})
	if err != nil || charlyCmd == "" {
		return fmt.Errorf("could not provide an invokable charly on the target %s for the D-Bus call: %v", ex.Venue(), err)
	}
	parts := []string{charlyCmd, "eval", "dbus", "call", ".",
		deployShellQuote(dest), deployShellQuote(path), deployShellQuote(method)}
	for _, a := range args {
		parts = append(parts, deployShellQuote(a))
	}
	return venueRun(ex, strings.Join(parts, " "))
}

// --- Argument parsing ---

// parseDbusArgs parses typed D-Bus arguments like "string:hello", "uint32:0", "boolean:true".
func parseDbusArgs(args []string) ([]interface{}, error) {
	var result []interface{}
	for _, arg := range args {
		idx := strings.IndexByte(arg, ':')
		if idx < 0 {
			return nil, fmt.Errorf("invalid argument %q: expected type:value (e.g. string:hello, uint32:0)", arg)
		}
		typ := arg[:idx]
		val := arg[idx+1:]
		parsed, err := parseDbusTypedValue(typ, val)
		if err != nil {
			return nil, fmt.Errorf("argument %q: %w", arg, err)
		}
		result = append(result, parsed)
	}
	return result, nil
}

func parseDbusTypedValue(typ, val string) (interface{}, error) {
	switch typ {
	case "string":
		return val, nil
	case "uint32":
		n, err := strconv.ParseUint(val, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid uint32: %s", val)
		}
		return uint32(n), nil
	case "int32":
		n, err := strconv.ParseInt(val, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid int32: %s", val)
		}
		return int32(n), nil
	case "boolean", "bool":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("invalid boolean: %s", val)
		}
		return b, nil
	case "int64":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid int64: %s", val)
		}
		return n, nil
	case "uint64":
		n, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid uint64: %s", val)
		}
		return n, nil
	case "double":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid double: %s", val)
		}
		return f, nil
	default:
		return nil, fmt.Errorf("unsupported type %q (supported: string, uint32, int32, int64, uint64, boolean, double)", typ)
	}
}

// --- Error helpers ---

func dbusNoBusError() error {
	return fmt.Errorf("cannot connect to D-Bus session bus\n" +
		"  DBUS_SESSION_BUS_ADDRESS is not set or the bus is not running.\n" +
		"  Ensure the 'dbus' layer is included in your image and the dbus service is started.\n" +
		"  Check with: charly service status <image> dbus")
}

func dbusNoToolError(venue string) error {
	return fmt.Errorf("cannot send D-Bus call on target %s — charly could not be provided (copy-in failed) and 'gdbus' is not installed\n"+
		"  Ensure glib2 is present (provides gdbus) for the fallback path.", venue)
}
