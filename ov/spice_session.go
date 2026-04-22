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

// spiceConnector satisfies spice.Connector by dialing a fixed TCP
// endpoint. Compress is passed through as a hint to the library
// (used for display channel feature negotiation).
type spiceConnector struct {
	addr string
}

func (c *spiceConnector) SpiceConnect(compress bool) (net.Conn, error) {
	_ = compress
	return net.DialTimeout("tcp", c.addr, 10*time.Second)
}

// spiceDriver captures display/cursor updates into concurrent-safe
// fields. The CLI reads them back for screenshot/cursor verbs.
type spiceDriver struct {
	mu          sync.Mutex
	displayImg  image.Image
	cursorImg   image.Image
	cursorX     uint16
	cursorY     uint16
	inputsRef   *spice.ChInputs
	mainRef     *spice.ChMain
	clipWanted  map[spice.SpiceClipboardSelection][]spice.SpiceClipboardFormat
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
// high-level verbs the CLI uses.
type SpiceSession struct {
	client *spice.Client
	driver *spiceDriver
	addr   string
}

// DialSpiceSession opens a SPICE connection to host:port with the
// given password. Password empty = AUTH_NONE; non-empty = SPICE_TICKET.
//
// Blocks on main-channel handshake + auth. Returns once channels are
// set up (other channels connect asynchronously inside the library).
func DialSpiceSession(host string, port int, passwd string) (*SpiceSession, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	conn := &spiceConnector{addr: addr}
	drv := newSpiceDriver()
	cli, err := spice.New(conn, drv, passwd)
	if err != nil {
		return nil, fmt.Errorf("spice handshake with %s: %w", addr, err)
	}
	return &SpiceSession{client: cli, driver: drv, addr: addr}, nil
}

// Close — Shells-com/spice has no explicit Close; rely on GC. We add
// this method to satisfy callers that expect cleanup, and to close
// stdlib resources if the driver gained any.
func (s *SpiceSession) Close() error {
	// Shells-com/spice doesn't expose Close; channels are backed by
	// net.Conn created inside SpiceConnect. Driver cleanup is GC.
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
