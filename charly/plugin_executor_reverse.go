package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/spec"
)

// executorReverseServer is the HOST side of the E3b reverse channel: it serves the
// proto ExecutorService by delegating to a live DeployExecutor (ShellExecutor /
// SSHExecutor / NestedExecutor). A deploy/step/builder provider's executor holds live
// OS resources (shell FDs, SSH connections) that cannot cross a process boundary, so
// an OUT-OF-PROCESS plugin never holds it — instead the host stands one of these up on
// the go-plugin GRPCBroker per deploy Invoke, passes its broker id in
// InvokeRequest.executor_broker_id, and the plugin dials back to run ops on the real
// venue. Built-in providers use the typed DeployExecutor directly (no wire).
type executorReverseServer struct {
	pb.UnimplementedExecutorServiceServer
	exec DeployExecutor
	// build is the host BUILD-ENGINE context (project Config + dir) the RunHostStep host-engine
	// leg needs to run a BuilderStep's host build (EnsureImagePresent + BuilderRun resolve
	// a short / namespace-qualified builder image and fall back to a local `charly box
	// build`). Zero value for a verb/kind/deploy Invoke that never drives RunHostStep.
	build buildEngineContext
}

func (s *executorReverseServer) Venue(context.Context, *pb.Empty) (*pb.VenueReply, error) {
	return &pb.VenueReply{Venue: s.exec.Venue()}, nil
}

func (s *executorReverseServer) RunSystem(ctx context.Context, req *pb.RunRequest) (*pb.RunReply, error) {
	return runReply(s.exec.RunSystem(ctx, req.GetScript(), decodeReverseEmitOpts(req.GetOptsJson())))
}

func (s *executorReverseServer) RunUser(ctx context.Context, req *pb.RunRequest) (*pb.RunReply, error) {
	return runReply(s.exec.RunUser(ctx, req.GetScript(), decodeReverseEmitOpts(req.GetOptsJson())))
}

// PutFile is the deploy/step file-PLACEMENT leg: an OUT-OF-PROCESS deploy/step plugin
// that EXECUTES an InstallPlan's steps pushes file content (a service unit, an env.d
// file, the charly binary, a builder artifact) onto the venue. The plugin holds no
// venue filesystem across the process boundary, so it ships the bytes; the host
// materializes them to a private temp file and delegates to the live
// DeployExecutor.PutFile (a plain os.WriteFile for ShellExecutor, scp+install for
// SSHExecutor), preserving the owner_root → root:root semantics. The gRPC call itself
// succeeds; a placement failure travels in PutFileReply.Error (the runReply convention).
func (s *executorReverseServer) PutFile(ctx context.Context, req *pb.PutFileRequest) (*pb.PutFileReply, error) {
	tmp, err := os.CreateTemp("", "charly-putfile-*")
	if err != nil {
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck
	if _, err := tmp.Write(req.GetContent()); err != nil {
		_ = tmp.Close()
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	if err := tmp.Close(); err != nil {
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	err = s.exec.PutFile(ctx, tmpPath, req.GetPath(), req.GetMode(), req.GetOwnerRoot(), decodeReverseEmitOpts(req.GetOptsJson()))
	return &pb.PutFileReply{Error: errString(err)}, nil
}

// RunCapture is the CHECK-VERB capture leg: an out-of-process exec-based check verb
// (record/dbus — and wl when it externalizes) probes the live venue by capturing
// stdout/stderr/exit. No root
// escalation — the verb's script adds sudo if it needs it. The gRPC call itself
// succeeds; an execution failure (not a non-zero exit) travels in CaptureReply.Error.
func (s *executorReverseServer) RunCapture(ctx context.Context, req *pb.RunRequest) (*pb.CaptureReply, error) {
	stdout, stderr, exit, err := s.exec.RunCapture(ctx, req.GetScript())
	return &pb.CaptureReply{Stdout: stdout, Stderr: stderr, ExitCode: int32(exit), Error: errString(err)}, nil
}

// GetFile is the CHECK-VERB artifact-pull leg: a verb that produces a file on the venue
// (a record .cast / a screenshot) reads it back to the host. asRoot reads via sudo.
func (s *executorReverseServer) GetFile(ctx context.Context, req *pb.GetFileRequest) (*pb.GetFileReply, error) {
	content, err := s.exec.GetFile(ctx, req.GetPath(), req.GetAsRoot(), decodeReverseEmitOpts(req.GetOptsJson()))
	return &pb.GetFileReply{Content: content, Error: errString(err)}, nil
}

// RunHostStep is the HOST-ENGINE channel leg (the generalization of the former F3 build channel): an
// OUT-OF-PROCESS deploy/step plugin walking an InstallPlan hits one of the five step kinds
// it CANNOT execute itself because each needs in-core host machinery that cannot move into
// the leaf plugin/kit package — BuilderStep (podman / BuilderRun / EnsureImagePresent),
// LocalPkgInstallStep (makepkg + pacman/dnf/apt), SystemPackagesStep (the format's
// phase.install.host template, rendered from the project DistroConfig), an act-verb OpStep
// (a builtin ProvisionActor shell that needs the in-proc provider registry), or an
// ExternalPluginStep (a verb served by ANOTHER out-of-process plugin, dispatched over a
// NESTED reverse channel). The plugin dials back here; the host reconstructs the concrete
// step from its serializable view, runs the EXISTING in-core machinery on the host (the
// SAME helpers the in-proc deploy targets use — no second implementation, R3), applies the
// effect onto the venue via s.exec, and returns the step's recorded ReverseOps so the
// plugin folds them into its DeployReply (record-and-replay teardown). Every OTHER
// (plugin-renderable) kind the plugin EXECUTES itself via the RunSystem/RunUser/PutFile
// legs — reaching RunHostStep with one is a plugin-walk bug (loud error). The plugin owns
// the plan WALK ordering; the host owns the host ENGINE. A host-engine/apply failure rides
// the reply's error field (the RPC itself succeeds, like runReply).
func (s *executorReverseServer) RunHostStep(ctx context.Context, req *pb.HostStepRequest) (*pb.HostStepReply, error) {
	var view spec.InstallStepView
	if err := json.Unmarshal(req.GetStepJson(), &view); err != nil {
		return &pb.HostStepReply{Error: fmt.Sprintf("decode step view: %v", err)}, nil
	}
	step, err := stepFromView(view)
	if err != nil {
		return &pb.HostStepReply{Error: err.Error()}, nil
	}
	opts := decodeReverseEmitOpts(req.GetOptsJson())

	var reverseOps []ReverseOp
	switch st := step.(type) {
	case *BuilderStep:
		venueHome, herr := s.exec.ResolveHome(ctx, "")
		if herr != nil {
			return &pb.HostStepReply{Error: fmt.Sprintf("resolve venue home: %v", herr)}, nil
		}
		if rerr := runVenueBuilderStep(ctx, s.exec, venueHome, s.build, st, opts); rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		reverseOps = st.Reverse()
	case *LocalPkgInstallStep:
		supported := venueHasPkgManager(ctx, s.exec, st.LocalPkg, opts)
		if rerr := execLocalPkgInstall(ctx, s.exec, st, supported, s.exec.Venue(), opts); rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		reverseOps = st.Reverse()
	case *SystemPackagesStep:
		// The format's phase.install.host template lives in the resolved DistroConfig the
		// plugin cannot reach — render it host-side (the SAME renderHostPackageCommand the
		// in-proc local deploy target/VmDeployTarget use, R3) and RunSystem on the venue.
		cmd, rerr := renderHostPackageCommand(s.build.DistroCfg, st)
		if rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		if cmd != "" { // empty = no host render for this phase (a clean no-op, not an error)
			if rerr := s.exec.RunSystem(ctx, cmd, opts); rerr != nil {
				return &pb.HostStepReply{Error: fmt.Sprintf("system packages %s: %v", st.Format, rerr)}, nil
			}
		}
		reverseOps = st.Reverse()
	case *OpStep:
		// An act-verb OpStep (a `run: plugin: <verb>` whose builtin ProvisionActor shell
		// needs the in-proc registry). resolveProvisionScript is the SAME Op→act-shell seam
		// the in-proc deploy path (renderOpCommand) uses (R3). A NON-act OpStep
		// (mkdir/copy/write/link/setcap/download/cmd/plugin:command) is plugin-renderable and
		// must NOT arrive here — ok=false is a loud plugin-walk bug.
		script, ok := resolveProvisionScript(st.Op, st.Distros)
		if !ok {
			return &pb.HostStepReply{Error: fmt.Sprintf("RunHostStep: OpStep verb is not act-capable (a plugin-renderable OpStep must be executed by the plugin via RunSystem/RunUser, not routed to RunHostStep)")}, nil
		}
		runErr := s.exec.RunUser(ctx, script, opts)
		if st.Scope() == ScopeSystem {
			runErr = s.exec.RunSystem(ctx, script, opts)
		}
		if runErr != nil {
			return &pb.HostStepReply{Error: runErr.Error()}, nil
		}
		reverseOps = st.Reverse()
	case *ExternalPluginStep:
		// A verb served by ANOTHER out-of-process plugin — the host stands up a SECOND
		// reverse channel on THAT plugin's broker (a nested reverse channel, delegating to
		// the SAME venue executor s.exec) and Invokes its OpExecute. executeExternalPluginStep
		// is the SAME seam the in-proc deploy targets use (R3); plan is nil (the venue-name
		// derivation only seeds a deterministic scratch dir, which the verb's plugin_input
		// already supplies). The nested plugin's teardown ReverseOps ride the reply.
		reply, rerr := executeExternalPluginStep(ctx, st, nil, s.exec, s.build)
		if rerr != nil {
			return &pb.HostStepReply{Error: rerr.Error()}, nil
		}
		reverseOps = reply.ReverseOps
	default:
		// Only the five host-engine step kinds route here. Every other (plugin-renderable)
		// kind the plugin EXECUTES itself via the RunSystem/RunUser/PutFile legs — reaching
		// RunHostStep with one is a plugin-walk bug.
		return &pb.HostStepReply{Error: fmt.Sprintf("RunHostStep: step kind %q is not a host-engine step (only Builder / LocalPkgInstall / SystemPackages / act-verb Op / ExternalPlugin route through the host-engine channel; execute every other kind via RunSystem/RunUser/PutFile)", view.Kind)}, nil
	}

	revJSON, err := json.Marshal(reverseOps)
	if err != nil {
		return &pb.HostStepReply{Error: fmt.Sprintf("marshal reverse ops: %v", err)}, nil
	}
	return &pb.HostStepReply{ReverseOpsJson: revJSON}, nil
}

// errString is err.Error() or "" — the reverse-channel convention (the RPC succeeds; the
// venue-op failure rides the reply's error field, like runReply).
func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// decodeReverseEmitOpts decodes the JSON EmitOpts carried in a RunRequest; an empty
// payload yields the zero EmitOpts (the common "no options" call).
func decodeReverseEmitOpts(b []byte) EmitOpts {
	var o EmitOpts
	if len(b) > 0 {
		_ = json.Unmarshal(b, &o)
	}
	return o
}

// runReply maps a DeployExecutor error onto a RunReply — the gRPC call itself
// succeeds; the script's error (if any) travels in the reply so the plugin sees the
// same string the in-proc executor would have returned.
func runReply(err error) (*pb.RunReply, error) {
	if err != nil {
		return &pb.RunReply{Error: err.Error()}, nil
	}
	return &pb.RunReply{}, nil
}
