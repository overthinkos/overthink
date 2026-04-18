package main

import (
	"reflect"
	"testing"
)

// Covers the build-time subset: every ImageMetadata field that should populate
// into Env, and negative checks for runtime-only vars (must not be present).
func TestResolveTestVarsBuild(t *testing.T) {
	meta := &ImageMetadata{
		Image:     "redis-ml",
		User:      "user",
		Home:      "/home/user",
		UID:       1000,
		GID:       1000,
		DNS:       "redis.example.com",
		AcmeEmail: "ops@example.com",
	}
	r := ResolveTestVarsBuild(meta)

	if r.HasRuntime {
		t.Error("build-time resolver must not claim runtime")
	}
	want := map[string]string{
		"IMAGE":      "redis-ml",
		"USER":       "user",
		"HOME":       "/home/user",
		"UID":        "1000",
		"GID":        "1000",
		"DNS":        "redis.example.com",
		"ACME_EMAIL": "ops@example.com",
	}
	if !reflect.DeepEqual(r.Env, want) {
		t.Errorf("env mismatch\ngot:  %v\nwant: %v", r.Env, want)
	}
	// Runtime-only keys must be absent.
	for _, key := range []string{"CONTAINER_IP", "CONTAINER_NAME", "HOST_PORT:6379", "VOLUME_PATH:workspace", "ENV_TOKEN"} {
		if _, ok := r.Env[key]; ok {
			t.Errorf("build-time resolver should not populate %q", key)
		}
	}
}

// Nil metadata must not panic; result is an empty map.
func TestResolveTestVarsBuild_Nil(t *testing.T) {
	r := ResolveTestVarsBuild(nil)
	if r == nil {
		t.Fatal("nil resolver")
	}
	if len(r.Env) != 0 {
		t.Errorf("expected empty env, got %v", r.Env)
	}
}

// Exercises the full runtime path with a swapped InspectContainer. Confirms
// each variable category: container name/IP, HOST_PORT mapping, VOLUME_PATH
// (both named-volume and bind-mount lookup paths), and ENV_<NAME>.
func TestResolveTestVarsRuntime(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()

	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return &ContainerInspection{
			Name: "/ov-redis-ml",
			Config: InspectConfig{
				Hostname: "ov-redis-ml",
				Env: []string{
					"REDIS_URL=redis://127.0.0.1:6379",
					"TOKEN=secret",
					"EMPTY=",
					"NOEQUALS", // should be ignored
				},
			},
			NetworkSettings: InspectNetwork{
				Networks: map[string]InspectNetworkBind{
					"ov": {IPAddress: "10.88.0.12"},
				},
				Ports: map[string][]InspectPortBind{
					"6379/tcp": {{HostIp: "0.0.0.0", HostPort: "16379"}},
					"9000/tcp": {}, // declared but unpublished → skipped
				},
			},
			Mounts: []InspectMount{
				{
					Type:        "volume",
					Name:        "ov-redis-ml-data",
					Source:      "/var/lib/containers/storage/volumes/ov-redis-ml-data/_data",
					Destination: "/data",
				},
				{
					Type:        "bind",
					Source:      "/tmp/ws-test",
					Destination: "/home/user/workspace",
				},
			},
		}, nil
	}

	meta := &ImageMetadata{
		Image: "redis-ml",
		User:  "user",
		Home:  "/home/user",
		Volumes: []VolumeMount{
			{VolumeName: "ov-redis-ml-data", ContainerPath: "/data"},
			{VolumeName: "ov-redis-ml-workspace", ContainerPath: "/home/user/workspace"},
		},
	}

	r, err := ResolveTestVarsRuntime(meta, nil, "podman", "ov-redis-ml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.HasRuntime {
		t.Error("HasRuntime should be true when inspect succeeds")
	}

	cases := []struct {
		key, want string
	}{
		{"IMAGE", "redis-ml"},
		{"CONTAINER_NAME", "ov-redis-ml"},
		{"CONTAINER_IP", "10.88.0.12"},
		{"HOST_PORT:6379", "16379"},
		{"VOLUME_PATH:data", "/var/lib/containers/storage/volumes/ov-redis-ml-data/_data"},
		{"VOLUME_CONTAINER_PATH:data", "/data"},
		{"VOLUME_PATH:workspace", "/tmp/ws-test"},
		{"VOLUME_CONTAINER_PATH:workspace", "/home/user/workspace"},
		{"ENV_REDIS_URL", "redis://127.0.0.1:6379"},
		{"ENV_TOKEN", "secret"},
		{"ENV_EMPTY", ""},
	}
	for _, tc := range cases {
		got, ok := r.Env[tc.key]
		if !ok {
			t.Errorf("missing key %q", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}

	// 9000 declared but unpublished — must not appear.
	if _, ok := r.Env["HOST_PORT:9000"]; ok {
		t.Error("unpublished port should not produce HOST_PORT")
	}
	// Malformed env without '=' must not appear.
	if _, ok := r.Env["ENV_NOEQUALS"]; ok {
		t.Error("malformed env line should be skipped")
	}
}

// If inspect fails, the resolver still returns the build-time subset and
// HasRuntime=false so the caller can report skip reasons on runtime vars.
func TestResolveTestVarsRuntime_InspectFails(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()

	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return nil, errStub("inspect failed")
	}

	meta := &ImageMetadata{Image: "jupyter", User: "user", Home: "/home/user"}
	r, err := ResolveTestVarsRuntime(meta, nil, "podman", "ov-jupyter")
	if err == nil {
		t.Fatal("expected error from failed inspect")
	}
	if r == nil {
		t.Fatal("resolver should be non-nil even on error")
	}
	if r.HasRuntime {
		t.Error("HasRuntime should be false when inspect failed")
	}
	if r.Env["IMAGE"] != "jupyter" {
		t.Errorf("build-time vars should still be populated, got %v", r.Env)
	}
	if _, ok := r.Env["CONTAINER_IP"]; ok {
		t.Error("runtime vars must not be populated on failure")
	}
}

// Top-level IPAddress (docker bridge) path.
func TestRuntimeVars_DockerBridgeIP(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()

	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return &ContainerInspection{
			Name: "/foo",
			NetworkSettings: InspectNetwork{
				IPAddress: "172.17.0.2",
			},
		}, nil
	}
	r, _ := ResolveTestVarsRuntime(&ImageMetadata{Image: "foo"}, nil, "docker", "foo")
	if r.Env["CONTAINER_IP"] != "172.17.0.2" {
		t.Errorf("docker-style top-level IP not picked up: %v", r.Env)
	}
}

// Exercises the full substitution pipeline: resolver output feeds ExpandTestVars.
func TestResolver_EndToEndExpansion(t *testing.T) {
	origInspect := InspectContainer
	defer func() { InspectContainer = origInspect }()

	InspectContainer = func(engine, name string) (*ContainerInspection, error) {
		return &ContainerInspection{
			Name: "/ov-redis",
			NetworkSettings: InspectNetwork{
				Networks: map[string]InspectNetworkBind{"ov": {IPAddress: "10.0.0.5"}},
				Ports: map[string][]InspectPortBind{
					"6379/tcp": {{HostPort: "16379"}},
				},
			},
		}, nil
	}
	r, err := ResolveTestVarsRuntime(&ImageMetadata{Image: "redis", User: "u", Home: "/home/u"}, nil, "podman", "ov-redis")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	in := "redis-cli -h ${CONTAINER_IP} -p ${HOST_PORT:6379} ${HOME}/.rdb"
	got, missing := ExpandTestVars(in, r.Env)
	want := "redis-cli -h 10.0.0.5 -p 16379 /home/u/.rdb"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if len(missing) != 0 {
		t.Errorf("unexpected missing: %v", missing)
	}
}

// errStub is a tiny error type so tests don't pull in fmt just to build one.
type errStub string

func (e errStub) Error() string { return string(e) }
