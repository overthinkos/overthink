package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"
)

// DbusCmd interacts with D-Bus services inside containers.
type DbusCmd struct {
	Notify     DbusNotifyCmd     `cmd:"" help:"Send a desktop notification"`
	Call       DbusCallCmd       `cmd:"" help:"Call a D-Bus method"`
	List       DbusListCmd       `cmd:"" help:"List available D-Bus services"`
	Introspect DbusIntrospectCmd `cmd:"" help:"Introspect a D-Bus service object"`
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return dbusNotifyRemoteStrict(engine, name, c.Title, c.Body)
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return dbusCallRemote(engine, name, c.Dest, c.Path, c.Method, c.Args)
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return dbusCallRemote(engine, name, "org.freedesktop.DBus", "/org/freedesktop/DBus",
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return dbusCallRemote(engine, name, c.Dest, c.Path,
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
		"ov",                          // app_name
		uint32(0),                     // replaces_id
		"",                            // app_icon
		title,                         // summary
		body,                          // body
		[]string{},                    // actions
		map[string]dbus.Variant{},     // hints
		int32(-1),                     // expire_timeout (-1 = default)
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

// dbusNotifyRemoteStrict sends a notification to a container, returning errors (for ov dbus notify).
func dbusNotifyRemoteStrict(engine, name, title, body string) error {
	// Try native ov binary first
	if checkToolAvailable(engine, name, "ov") == nil {
		cmd := exec.Command(engine, "exec", name, "ov", "dbus", "notify", ".", title, body)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "Warning: in-container ov dbus failed, trying gdbus fallback\n")
	} else {
		fmt.Fprintf(os.Stderr, "Warning: ov binary not found in container, falling back to gdbus\n"+
			"  For native D-Bus support, add the 'ov' layer to your image.\n")
	}

	// Fallback: gdbus call inside container
	if checkToolAvailable(engine, name, "gdbus") != nil {
		return dbusNoToolError(name)
	}
	gdbusCmd := fmt.Sprintf(
		`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && `+
			`gdbus call --session `+
			`--dest=org.freedesktop.Notifications `+
			`--object-path=/org/freedesktop/Notifications `+
			`--method=org.freedesktop.Notifications.Notify `+
			`"ov" 0 "" %s %s "[]" "{}" -- -1`,
		shellQuote(title), shellQuote(body))
	cmd := exec.Command(engine, "exec", name, "sh", "-c", gdbusCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// dbusCallRemote delegates a D-Bus call to the container's ov binary.
func dbusCallRemote(engine, name, dest, path, method string, args []string) error {
	// Try native ov binary first
	if checkToolAvailable(engine, name, "ov") == nil {
		ovArgs := []string{"exec", name, "ov", "dbus", "call", ".", dest, path, method}
		ovArgs = append(ovArgs, args...)
		cmd := exec.Command(engine, ovArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("ov binary not found in container %s\n"+
		"  Generic D-Bus calls require the 'ov' layer.\n"+
		"  For notifications only, 'gdbus' (from glib2) can be used as a fallback.", name)
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
		"  Check with: ov service status <image> dbus")
}

func dbusNoToolError(containerName string) error {
	return fmt.Errorf("cannot send D-Bus call — neither 'ov' nor 'gdbus' found in container %s\n"+
		"  Add the 'ov' layer (native D-Bus) or ensure glib2 is installed (provides gdbus).", containerName)
}
