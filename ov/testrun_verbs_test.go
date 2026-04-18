package main

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
)

// Package verb — installed-present, version match, and absent-as-expected paths.
func TestRunner_Package(t *testing.T) {
	t.Run("installed via rpm", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "rpm -q 'redis'", exit: 0},
		}
		res := r.Run(context.Background(), []Check{{Package: "redis", Installed: ptrBool(true)}})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("installed mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "rpm -q 'redis'", exit: 1},
		}
		res := r.Run(context.Background(), []Check{{Package: "redis", Installed: ptrBool(true)}})
		if res[0].Status != TestFail {
			t.Errorf("expected fail, got %+v", res[0])
		}
	})

	t.Run("version list match", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "rpm -q 'redis' >/dev/null", exit: 0},
			{matchPrefix: "rpm -q --qf '%{VERSION}", stdout: "7.0.5\n", exit: 0},
		}
		res := r.Run(context.Background(), []Check{
			{Package: "redis", Versions: []string{"7.0.5", "7.0.6"}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})
}

// Service verb — running true/false and enabled attribute.
func TestRunner_Service(t *testing.T) {
	t.Run("running true via supervisorctl", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "supervisorctl status 'jupyter'", exit: 0},
		}
		res := r.Run(context.Background(), []Check{{Service: "jupyter", Running: ptrBool(true)}})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("running mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "supervisorctl status 'jupyter'", exit: 1},
		}
		res := r.Run(context.Background(), []Check{{Service: "jupyter", Running: ptrBool(true)}})
		if res[0].Status != TestFail {
			t.Errorf("expected fail, got %+v", res[0])
		}
	})
}

// Process verb — pgrep exit status.
func TestRunner_Process(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{{matchPrefix: "pgrep -x 'redis-server'", exit: 0}}
		res := r.Run(context.Background(), []Check{{Process: "redis-server"}})
		if res[0].Status != TestPass {
			t.Errorf("got %+v", res[0])
		}
	})
	t.Run("expected absent", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{{matchPrefix: "pgrep -x 'worm'", exit: 1}}
		res := r.Run(context.Background(), []Check{{Process: "worm", Running: ptrBool(false)}})
		if res[0].Status != TestPass {
			t.Errorf("got %+v", res[0])
		}
	})
}

// DNS verb — host-side resolution for a guaranteed-resolvable hostname,
// and expected-unresolvable for a never-assigned TLD.
func TestRunner_DNS(t *testing.T) {
	t.Run("resolvable localhost", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeTest)
		res := r.Run(context.Background(), []Check{{DNS: "localhost"}})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})
	t.Run("unresolvable as expected", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeTest)
		res := r.Run(context.Background(), []Check{
			{DNS: "this-host-will-never-exist.invalid", Resolvable: ptrBool(false)},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})
}

// user verb — getent output parsing.
func TestRunner_User(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeTest)
	fake.responses = []fakeResponse{
		{matchPrefix: "getent passwd 'alice'", stdout: "alice:x:1000:1000:Alice:/home/alice:/bin/bash\n", exit: 0},
	}
	uid := 1000
	res := r.Run(context.Background(), []Check{{User: "alice", UID: &uid, Home: "/home/alice"}})
	if res[0].Status != TestPass {
		t.Errorf("expected pass, got %+v", res[0])
	}
}

func TestRunner_User_UIDMismatch(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeTest)
	fake.responses = []fakeResponse{
		{matchPrefix: "getent passwd 'alice'", stdout: "alice:x:1500:1500:Alice:/home/alice:/bin/bash\n", exit: 0},
	}
	uid := 1000
	res := r.Run(context.Background(), []Check{{User: "alice", UID: &uid}})
	if res[0].Status != TestFail {
		t.Errorf("expected fail, got %+v", res[0])
	}
}

// group verb — getent group parsing.
func TestRunner_Group(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeTest)
	fake.responses = []fakeResponse{
		{matchPrefix: "getent group 'docker'", stdout: "docker:x:999:alice,bob\n", exit: 0},
	}
	gid := 999
	res := r.Run(context.Background(), []Check{{Group: "docker", GID: &gid}})
	if res[0].Status != TestPass {
		t.Errorf("got %+v", res[0])
	}
}

// interface verb — presence + MTU check.
func TestRunner_Interface(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeTest)
	fake.responses = []fakeResponse{
		{matchPrefix: "ip -o addr show 'eth0'", stdout: "2: eth0 inet 10.0.0.5/24\n", exit: 0},
		{matchPrefix: "ip -o link show 'eth0'", stdout: "1500\n", exit: 0},
	}
	mtu := 1500
	res := r.Run(context.Background(), []Check{
		{Interface: "eth0", MTU: &mtu, Addrs: []string{"10.0.0.5/24"}},
	})
	if res[0].Status != TestPass {
		t.Errorf("got %+v", res[0])
	}
}

// kernel-param verb — scalar match via Matching.
func TestRunner_KernelParam(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeTest)
	fake.responses = []fakeResponse{
		{matchPrefix: "sysctl -n 'net.ipv4.ip_forward'", stdout: "1\n", exit: 0},
	}
	res := r.Run(context.Background(), []Check{
		{KernelParam: "net.ipv4.ip_forward", Value: MatcherList{{Op: "equals", Value: "1"}}},
	})
	if res[0].Status != TestPass {
		t.Errorf("got %+v", res[0])
	}
}

// mount verb — findmnt parsing + filesystem check.
func TestRunner_Mount(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeTest)
	fake.responses = []fakeResponse{
		{matchPrefix: "findmnt -n -o SOURCE,FSTYPE,OPTIONS '/data'",
			stdout: "/dev/sda1 ext4 rw,relatime\n", exit: 0},
	}
	res := r.Run(context.Background(), []Check{
		{Mount: "/data", Filesystem: "ext4", MountSource: "/dev/sda1"},
	})
	if res[0].Status != TestPass {
		t.Errorf("got %+v", res[0])
	}
}

// addr verb — host-side dial against a real httptest listener.
func TestRunner_Addr(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	// Extract host:port from the test server URL.
	u := strings.TrimPrefix(srv.URL, "http://")

	r, _ := newFakeRunner(t, RunModeTest)
	res := r.Run(context.Background(), []Check{{Addr: u}})
	if res[0].Status != TestPass {
		t.Errorf("expected reachable, got %+v", res[0])
	}

	// Unreachable — pick a high port nothing is on. net.Listen gives us one safely.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close() // free the port
	res = r.Run(context.Background(), []Check{
		{Addr: addr, Reachable: ptrBool(false)},
	})
	if res[0].Status != TestPass {
		t.Errorf("expected unreachable-as-expected, got %+v", res[0])
	}
}

// matching verb — pure in-process value matching.
func TestRunner_Matching(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeTest)
	res := r.Run(context.Background(), []Check{
		{Matching: "hello world", Contains: MatcherList{{Op: "contains", Value: "world"}}},
	})
	if res[0].Status != TestPass {
		t.Errorf("got %+v", res[0])
	}
}
