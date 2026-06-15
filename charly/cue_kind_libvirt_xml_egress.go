package main

// Registers the (package-less, shared-scope) koala-shaped egress schema for the
// rendered libvirt domain XML.
func init() { registerCueKind("libvirt_domain_xml", "#LibvirtDomainXML") }
