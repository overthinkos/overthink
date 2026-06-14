package main

// Per-kind registration for `candy` (independent file — the unit the parallel
// Implement fan-out owns). Schema #Candy in schema/candy.cue (embedded centrally
// by cue_schema.go).

func init() {
	registerCueKind("candy", "#Candy")
}
