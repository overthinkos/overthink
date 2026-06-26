package main

// egress_stub.go is a TRANSITIONAL Phase-A shim. The moved cloud-init / libvirt-XML generators
// called ValidateEgress / ValidateXMLEgress inline (charly/egress.go, with vendored CUE schemas,
// NO go-libvirt). The out-of-process plugin must not carry the egress subsystem; in Phase B the
// HOST egress-validates the XML/cloud-init the plugin RETURNS over the RPC. DELETE this stub +
// move the validation host-side BEFORE the R10 acceptance run (R5/Hard Cutover).
func ValidateEgress(_ string, _ string, _ []byte) error    { return nil }
func ValidateXMLEgress(_ string, _ string, _ string) error { return nil }
