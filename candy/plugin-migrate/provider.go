// Package migrate is the MIGRATION CHAIN plugin (C13a) — the single ordered
// `charly migrate` chain (the registry + every transformer) carved out of charly
// core. The plugin serves ONE full-chain op via Invoke: OpRun decodes a
// kit.MigrateContext (the project dir, per-host paths, flags, and the HOST-PRELIFTED
// loader inputs core resolved before invoking), runs the whole chain to HEAD, and
// returns a kit.MigrateReply ({changed, files, error}).
//
// Several migration steps need package-main loader machinery a candy cannot import
// (LoadUnified, LoadBundleConfig, mergeUnifiedDocs/embeddedDefaults, VerbCatalog,
// DeployConfigPath); charly's in-core charly/migrate.go shim host-prelifts those
// lookups into the MigrateContext, so the chain here is loader-free. Compiled-in
// (charly/charly.yml compiled_plugins) — the `charly migrate` command + the
// remote-cache auto-migration (refs.go) both call the in-core shim, which Invokes
// this plugin in-proc.
package migrate

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

//go:embed schema/*.cue
var describeSchemaFS embed.FS

const calver = "2026.181.0001"

// NewProvider builds the migrate provider.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct {
	pb.UnimplementedProviderServer
}

// Invoke handles OpRun: decode the kit.MigrateContext (with host-prelifted inputs),
// run the full migration chain, and return a kit.MigrateReply.
func (p *provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpRun {
		return nil, fmt.Errorf("migrate: unsupported op %q (only %q)", req.GetOp(), sdk.OpRun)
	}
	var ctx kit.MigrateContext
	if err := json.Unmarshal(req.GetParamsJson(), &ctx); err != nil {
		return nil, fmt.Errorf("migrate: decode context: %w", err)
	}
	// Out is never serialized — reconstruct it from Quiet. Compiled-in, os.Stderr
	// is the host's, so progress lines surface for `charly migrate`.
	if ctx.Quiet {
		ctx.Out = io.Discard
	} else {
		ctx.Out = os.Stderr
	}
	changed, files, err := runMigrations(&ctx, ctx.ProjectOnly)
	reply := kit.MigrateReply{Changed: changed, Files: files}
	if err != nil {
		reply.Error = err.Error()
	}
	out, merr := json.Marshal(reply)
	if merr != nil {
		return nil, merr
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises verb:migrate serving OpRun. The verb is invoked with the
// structured kit.MigrateContext over OpRun, not an authored plugin_input, so it
// declares no #*Input — Describe ships only the trivial #MigrateInput so the host's
// plugin-schema gate has a non-empty, base-spliceable schema.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "verb", Word: "migrate"}},
		describeSchemaFS, "schema")
}
