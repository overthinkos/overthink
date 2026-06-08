package main

// `charly eval vnc vm <name> <verb>` — RFB/VNC verbs targeting a VM
// declared in vm.yml, mirroring the shape of `charly eval spice`.
//
// For VMs whose <graphics type='vnc'> listens on a UNIX socket,
// we dial the socket directly (local) or tunnel it over SSH (remote);
// for TCP listeners we dial or tunnel TCP. The VNC client in
// vnc_client.go speaks over a TCP net.Conn, so UNIX sockets always
// pass through a local bridge listener opened here when needed.

import (
	"context"
	"encoding/hex"
	"fmt"
	"image/png"
	"io"
	"net"
	"os"
	"time"
)

// VncVmCmd groups the VM-targeted VNC verbs.
type VncVmCmd struct {
	Status     VncVmStatusCmd     `cmd:"" help:"Show VNC endpoint + framebuffer dimensions"`
	Screenshot VncVmScreenshotCmd `cmd:"" help:"Capture framebuffer as PNG"`
	Click      VncVmClickCmd      `cmd:"" help:"Click at x,y"`
	Key        VncVmKeyCmd        `cmd:"" help:"Press a named key"`
	Type       VncVmTypeCmd       `cmd:"" help:"Type text"`
	Mouse      VncVmMouseCmd      `cmd:"" help:"Move pointer to x,y (no click)"`
}

// vncVmFlags is the shared flag surface for every `charly eval vnc vm …`
// verb. --uri follows the same convention as `charly eval libvirt --uri`.
type vncVmFlags struct {
	Uri string `name:"uri" env:"CH_LIBVIRT_URI" help:"Libvirt URI (default: qemu:///session)."`
}

// vmVncSession is a live VNC client plus any bridge listener + SSH
// tunnel opened to reach it. Close tears everything down in order.
type vmVncSession struct {
	client *VNCClient
	bridge net.Listener // for UNIX-socket forwarding (nil when dialing TCP directly)
	tunnel *SSHTunnel   // optional SSH tunnel (nil when local)
}

func (s *vmVncSession) Close() {
	if s == nil {
		return
	}
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.bridge != nil {
		_ = s.bridge.Close()
	}
	if s.tunnel != nil {
		_ = s.tunnel.Close()
	}
}

// openVncForVm resolves the VNC endpoint for `vmName` and returns a
// live VNC client. Covers: TCP local, TCP remote (SSH-tunneled),
// UNIX-socket local (via a bridge listener), UNIX-socket remote
// (forwarded socket → bridge listener → TCP dial).
func openVncForVm(vmName, uri string) (*vmVncSession, error) {
	t, err := ResolveVmTarget(vmName, uri)
	if err != nil {
		return nil, err
	}
	ep, err := t.VncEndpoint()
	tunnelTarget := t.Uri
	t.Close()
	if err != nil {
		return nil, err
	}

	// Build a TCP address the VNCClient can dial: either directly, or
	// via a bridge listener that pipes bytes to a UNIX socket (local
	// or remote over SSH).
	switch {
	case !ep.TunnelNeeded && !ep.IsSocket:
		// Local TCP — straight dial.
		addr := fmt.Sprintf("%s:%d", ep.Host, ep.Port)
		cli, err := NewVNCClient(addr, ep.Password)
		if err != nil {
			return nil, err
		}
		return &vmVncSession{client: cli}, nil

	case !ep.TunnelNeeded && ep.IsSocket:
		// Local UNIX socket — bridge it to a TCP listener so the
		// VNCClient (TCP-only) can speak to it.
		bridge, err := unixToTcpBridge(ep.SocketPath)
		if err != nil {
			return nil, err
		}
		cli, err := NewVNCClient(bridge.Addr().String(), ep.Password)
		if err != nil {
			bridge.Close()
			return nil, err
		}
		return &vmVncSession{client: cli, bridge: bridge}, nil

	case ep.TunnelNeeded && ep.IsSocket:
		// Remote UNIX socket — SSH-forward it to a local UNIX socket,
		// then bridge to TCP.
		parsed, err := ParseLibvirtURI(tunnelTarget)
		if err != nil {
			return nil, err
		}
		tun, err := NewSSHTunnel(parsed.Remote)
		if err != nil {
			return nil, err
		}
		localSock, _, err := tun.ForwardUnix(context.Background(), ep.SocketPath)
		if err != nil {
			tun.Close()
			return nil, err
		}
		bridge, err := unixToTcpBridge(localSock)
		if err != nil {
			tun.Close()
			return nil, err
		}
		cli, err := NewVNCClient(bridge.Addr().String(), ep.Password)
		if err != nil {
			bridge.Close()
			tun.Close()
			return nil, err
		}
		return &vmVncSession{client: cli, bridge: bridge, tunnel: tun}, nil

	case ep.TunnelNeeded && !ep.IsSocket:
		// Remote TCP — SSH-forward it to a local TCP port, dial that.
		parsed, err := ParseLibvirtURI(tunnelTarget)
		if err != nil {
			return nil, err
		}
		tun, err := NewSSHTunnel(parsed.Remote)
		if err != nil {
			return nil, err
		}
		localAddr, _, err := tun.ForwardTCP(context.Background(), ep.Host, ep.Port)
		if err != nil {
			tun.Close()
			return nil, err
		}
		cli, err := NewVNCClient(localAddr, ep.Password)
		if err != nil {
			tun.Close()
			return nil, err
		}
		return &vmVncSession{client: cli, tunnel: tun}, nil
	}
	return nil, fmt.Errorf("unreachable: VNC endpoint resolution for %s produced no dial target", vmName)
}

// unixToTcpBridge starts a TCP listener on 127.0.0.1:0 that pipes
// each accepted connection to the named UNIX socket. Returned
// listener owns a goroutine that exits when the listener is closed.
func unixToTcpBridge(socketPath string) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bridge listen: %w", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				u, err := net.DialTimeout("unix", socketPath, 5*time.Second)
				if err != nil {
					return
				}
				defer u.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(u, c); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, u); done <- struct{}{} }()
				<-done
			}()
		}
	}()
	return ln, nil
}

// ---------------- status ----------------

type VncVmStatusCmd struct {
	Vm string `arg:"" help:"VM name"`
	vncVmFlags
}

func (c *VncVmStatusCmd) Run() error {
	s, err := openVncForVm(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer s.Close()
	fmt.Printf("connected: %s\n", s.client.DesktopName())
	fmt.Printf("framebuffer: %dx%d\n", s.client.Width(), s.client.Height())
	fmt.Printf("pixelformat: %d bpp depth=%d truecolor=%v\n",
		s.client.pixelFormat.BPP,
		s.client.pixelFormat.Depth,
		s.client.pixelFormat.TrueColor == 1,
	)
	return nil
}

// ---------------- screenshot ----------------

type VncVmScreenshotCmd struct {
	Vm   string `arg:"" help:"VM name"`
	File string `arg:"" optional:"" default:"vnc-screenshot.png" help:"Output file path (use '-' for stdout)"`
	vncVmFlags
}

func (c *VncVmScreenshotCmd) Run() error {
	s, err := openVncForVm(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer s.Close()
	img, err := s.client.Screenshot()
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}
	w := writerForPath(c.File)
	if closer, ok := w.(io.Closer); ok && c.File != "-" {
		defer closer.Close()
	}
	if err := png.Encode(w, img); err != nil {
		return fmt.Errorf("encode PNG: %w", err)
	}
	b := img.Bounds()
	dest := c.File
	if dest == "-" {
		dest = "stdout"
	}
	fmt.Fprintf(os.Stderr, "Screenshot saved to %s (%dx%d)\n", dest, b.Dx(), b.Dy())
	return nil
}

// ---------------- click ----------------

type VncVmClickCmd struct {
	Vm     string `arg:"" help:"VM name"`
	X      uint16 `arg:"" help:"X coordinate"`
	Y      uint16 `arg:"" help:"Y coordinate"`
	Button string `long:"button" default:"left" help:"left, middle, right"`
	vncVmFlags
}

func (c *VncVmClickCmd) Run() error {
	s, err := openVncForVm(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.client.PointerClick(c.X, c.Y, vncButton(c.Button))
}

// ---------------- mouse ----------------

type VncVmMouseCmd struct {
	Vm string `arg:"" help:"VM name"`
	X  uint16 `arg:"" help:"X coordinate"`
	Y  uint16 `arg:"" help:"Y coordinate"`
	vncVmFlags
}

func (c *VncVmMouseCmd) Run() error {
	s, err := openVncForVm(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.client.PointerMove(c.X, c.Y)
}

// ---------------- key ----------------

type VncVmKeyCmd struct {
	Vm  string `arg:"" help:"VM name"`
	Key string `arg:"" help:"Key name (e.g. Return, Escape, F2)"`
	vncVmFlags
}

func (c *VncVmKeyCmd) Run() error {
	s, err := openVncForVm(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer s.Close()
	k, ok := vncKeyMap[c.Key]
	if !ok {
		return fmt.Errorf("unknown key %q; known: %s", c.Key, vncKeyNames())
	}
	return s.client.KeyPress(k)
}

// ---------------- type ----------------

type VncVmTypeCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Text string `arg:"" help:"Text to type"`
	vncVmFlags
}

func (c *VncVmTypeCmd) Run() error {
	s, err := openVncForVm(c.Vm, c.Uri)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.client.TypeText(c.Text)
}

// unused import guard for files importing encoding/hex in other VM
// VNC tooling (future expansion — e.g. binary RFB dumps).
var _ = hex.EncodeToString
