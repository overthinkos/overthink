// schema/migrateinput.cue — the TRIVIAL Describe schema. The migrate verb is
// invoked with the structured kit.MigrateContext over OpRun, not an authored
// plugin_input, so it declares no #*Input. This def exists only to give the host
// plugin-schema gate a non-empty, base-spliceable schema.
#MigrateInput: {
	contract: "invoked via OpRun with a kit.MigrateContext (project dir + per-host paths + host-prelifted loader inputs); returns a kit.MigrateReply {changed, files, error}"
}
