package main

// Thin wrapper over github.com/Shells-com/spice for `ov test spice`.
//
// The upstream library needs:
//   - A Connector (we dial a TCP connection to the resolved SPICE host:port).
//   - A Driver (we capture display + cursor updates into fields we expose).
//   - A password (empty for AUTH_NONE).
//
// It spins up goroutines for each SPICE channel (main, display, inputs,
// cursor, playback, record, webdav). Our Driver is a stub that doesn't
// render — it just stashes the latest framebuffer into a field the
// CLI can read back.

import (
	"fmt"
	"image"
	"net"
	"sync"
	"time"

	spice "github.com/Shells-com/spice"
)

// spiceConnector satisfies spice.Connector by dialing a fixed SPICE
// endpoint. The endpoint can be a TCP host:port (classic) or a UNIX
// socket path (modern default for ov-managed VMs after the hard
// cutover — see vms.yml arch). Compress is passed through
// as a hint to the library.
type spiceConnector struct {
	network string // "tcp" | "unix"
	addr    string
}

func (c *spiceConnector) SpiceConnect(compress bool) (net.Conn, error) {
	_ = compress
	return net.DialTimeout(c.network, c.addr, 10*time.Second)
}

// spiceDriver captures display/cursor updates into concurrent-safe
// fields. The CLI reads them back for screenshot/cursor verbs.
type spiceDriver struct {
	mu         sync.Mutex
	displayImg image.Image
	cursorImg  image.Image
	cursorX    uint16
	cursorY    uint16
	inputsRef  *spice.ChInputs
	mainRef    *spice.ChMain
	clipWanted map[spice.SpiceClipboardSelection][]spice.SpiceClipboardFormat
}

func newSpiceDriver() *spiceDriver {
	return &spiceDriver{
		clipWanted: map[spice.SpiceClipboardSelection][]spice.SpiceClipboardFormat{},
	}
}

func (d *spiceDriver) DisplayInit(img image.Image) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.displayImg = img
}

func (d *spiceDriver) DisplayRefresh() {
	// No-op: we snapshot the image on demand via Screenshot().
}

func (d *spiceDriver) SetEventsTarget(in *spice.ChInputs) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inputsRef = in
}

func (d *spiceDriver) SetMainTarget(m *spice.ChMain) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mainRef = m
}

func (d *spiceDriver) SetCursor(img image.Image, x, y uint16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cursorImg = img
	d.cursorX = x
	d.cursorY = y
}

// Clipboard — minimal stubs. The agent-channel verbs use the main
// channel's clipboard methods directly via d.mainRef.
func (d *spiceDriver) ClipboardGrabbed(sel spice.SpiceClipboardSelection, types []spice.SpiceClipboardFormat) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clipWanted[sel] = types
}
func (d *spiceDriver) ClipboardFetch(sel spice.SpiceClipboardSelection, t spice.SpiceClipboardFormat) ([]byte, error) {
	return nil, fmt.Errorf("overthink spice driver does not provide clipboard data")
}
func (d *spiceDriver) ClipboardRelease(sel spice.SpiceClipboardSelection) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.clipWanted, sel)
}

// SpiceSession holds an open SPICE connection and provides the
// high-level verbs the CLI uses. When the session was established
// through an auto-opened SSH tunnel, `tunnel` is non-nil and Close()
// tears it down.
type SpiceSession struct {
	client *spice.Client
	driver *spiceDriver
	addr   string

	// tunnel is the optional SSH tunnel under the connection. nil
	// for local sessions (no forward needed).
	tunnel *SSHTunnel
}

// DialSpiceTCP opens a SPICE connection over TCP. Password empty =
// AUTH_NONE; non-empty = SPICE_TICKET. Blocks on main-channel
// handshake + auth.
func DialSpiceTCP(host string, port int, passwd string) (*SpiceSession, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn := &spiceConnector{network: "tcp", addr: addr}
	drv := newSpiceDriver()
	cli, err := spice.New(conn, drv, passwd)
	if err != nil {
		return nil, fmt.Errorf("spice handshake with %s: %w", addr, err)
	}
	return &SpiceSession{client: cli, driver: drv, addr: addr}, nil
}

// DialSpiceUnix opens a SPICE connection over a UNIX socket. This is
// the default for ov-managed VMs after the socket-listen cutover;
// virt-manager and remote-viewer auto-forward these over qemu+ssh://.
func DialSpiceUnix(path, passwd string) (*SpiceSession, error) {
	conn := &spiceConnector{network: "unix", addr: path}
	drv := newSpiceDriver()
	cli, err := spice.New(conn, drv, passwd)
	if err != nil {
		return nil, fmt.Errorf("spice handshake with unix://%s: %w", path, err)
	}
	return &SpiceSession{client: cli, driver: drv, addr: "unix://" + path}, nil
}

// DialSpiceSession is retained as a thin wrapper over DialSpiceTCP
// for any external callers — existing internal callers have been
// migrated to the explicit TCP/Unix constructors.
//
// Deprecated: use DialSpiceTCP or DialSpiceUnix directly.
func DialSpiceSession(host string, port int, passwd string) (*SpiceSession, error) {
	return DialSpiceTCP(host, port, passwd)
}

// Close tears down any auto-opened SSH tunnel. Shells-com/spice has
// no explicit Close for its channels; they rely on GC.
func (s *SpiceSession) Close() error {
	if s == nil {
		return nil
	}
	if s.tunnel != nil {
		return s.tunnel.Close()
	}
	return nil
}

// Display returns the latest framebuffer image, or nil if none
// received yet.
func (s *SpiceSession) Display() image.Image {
	s.driver.mu.Lock()
	defer s.driver.mu.Unlock()
	return s.driver.displayImg
}

// Cursor returns the current cursor bitmap + position.
func (s *SpiceSession) Cursor() (image.Image, uint16, uint16) {
	s.driver.mu.Lock()
	defer s.driver.mu.Unlock()
	return s.driver.cursorImg, s.driver.cursorX, s.driver.cursorY
}

// Inputs returns the inputs channel handle (may be nil if not yet set
// up). Methods on ChInputs: OnKeyDown, OnKeyUp, MouseDown, MouseUp,
// MousePosition.
func (s *SpiceSession) Inputs() *spice.ChInputs {
	s.driver.mu.Lock()
	defer s.driver.mu.Unlock()
	return s.driver.inputsRef
}

// WaitForDisplay blocks (with timeout) until a framebuffer has been
// delivered. Useful to let screenshots wait for the first render
// after connect.
func (s *SpiceSession) WaitForDisplay(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Display() != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("no display frame within %s", timeout)
}

// WaitForInputs blocks until the inputs channel is ready.
func (s *SpiceSession) WaitForInputs(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Inputs() != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("inputs channel not ready within %s", timeout)
}
