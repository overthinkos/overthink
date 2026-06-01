package main

import (
	"context"
	"os"
	"strings"
)

// VMCollector is the libvirt SubstrateCollector. It lists ov-* libvirt
// domains via listOvDomains(), maps each domain to a DeploymentStatus
// stamped Kind=SubstrateVM, Source="libvirt", and enriches the row from the
// matching target:vm deploy.yml entry's vm_state (SSH port/user, backend) when
// one exists. A domain with no deploy entry still shows (Source:libvirt) — the
// libvirt domain list is the source of truth for what is actually defined on
// the host, deploy.yml is only enrichment.
//
// Only LIBVIRT-backed domains surface here: listOvDomains() queries the
// session daemon, so a VM booted via the qemu backend (pidfile-tracked, not a
// libvirt domain) is not a VMCollector row. That matches `ov vm list`, where
// libvirt domains and qemu pidfiles are two distinct probes.
type VMCollector struct {
	c *Collector
}

// listLibvirtOvDomains lists ov-* libvirt domains. Swappable for tests
// (mirrors InspectContainer in evalvars.go) so the table-driven test can mock
// the libvirt listing without a live session daemon. The real implementation
// connects to the local session daemon, lists, and disconnects.
var listLibvirtOvDomains = defaultListLibvirtOvDomains

func defaultListLibvirtOvDomains() ([]domainInfo, error) {
	conn, err := connectLibvirt("")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.listOvDomains()
}

func init() {
	registerSubstrate(func(c *Collector) SubstrateCollector { return &VMCollector{c: c} })
}

// Kind reports the vm substrate.
func (v *VMCollector) Kind() SubstrateKind { return SubstrateVM }

// Available reports whether a libvirt session daemon is reachable on this host
// WITHOUT spinning one up. It stat()s the modular/legacy session socket via
// the shared libvirtSessionSocket() probe — the same path resolveVmBackend()
// uses for the "libvirt" backend — and reports reachable only when the socket
// file exists. An absent socket means no libvirt session, so the substrate is
// silently skipped (no rows, no error) per the SubstrateCollector contract.
func (v *VMCollector) Available(opts CollectOpts) bool {
	sock := libvirtSessionSocket()
	if sock == "" {
		return false
	}
	_, err := os.Stat(sock)
	return err == nil
}

// Collect lists ov-* libvirt domains and maps each to a DeploymentStatus.
// Rows are NOT pre-sorted here — Collector.All sorts the merged set across all
// substrates.
func (v *VMCollector) Collect(ctx context.Context, opts CollectOpts) ([]DeploymentStatus, error) {
	domains, err := listLibvirtOvDomains()
	if err != nil {
		return nil, err
	}
	rows := make([]DeploymentStatus, 0, len(domains))
	for _, d := range domains {
		rows = append(rows, v.rowForDomain(d, opts))
	}
	return rows, nil
}

// rowForDomain builds a DeploymentStatus for one libvirt domain. The domain
// name carries the canonical ov-<entity> shape; the entity name (ov- prefix
// stripped) is both the Image cell and the key used to find the matching
// target:vm deploy entry for vm_state enrichment.
func (v *VMCollector) rowForDomain(d domainInfo, opts CollectOpts) DeploymentStatus {
	entity := strings.TrimPrefix(d.Name, "ov-")
	cs := DeploymentStatus{
		Kind:      SubstrateVM,
		Source:    "libvirt",
		Image:     entity,
		Status:    vmStatusFromDomainState(d.State),
		Container: d.Name,
		RunMode:   opts.RunMode,
	}
	v.enrichFromDeploy(&cs, entity, opts)
	return cs
}

// enrichFromDeploy fills network/backend detail from the matching target:vm
// deploy entry's vm_state when one exists. The deploy entry is resolved via the
// shared findVmDeployNode (deploy-NAME first, then target:vm whose vm: ==
// entity) so a bed whose key differs from its vm entity is still matched.
// Absence of a deploy entry is normal: the libvirt domain still shows with
// Source:libvirt and no enrichment.
func (v *VMCollector) enrichFromDeploy(cs *DeploymentStatus, entity string, opts CollectOpts) {
	if opts.Deploy == nil || opts.Deploy.Deploy == nil {
		return
	}
	node, ok := findVmDeployNode(opts.Deploy.Deploy, entity, entity)
	if !ok {
		return
	}
	if node.Network != "" {
		cs.Network = node.Network
	}
	state := node.VmState
	if state == nil {
		return
	}
	// Surface the guest SSH endpoint as a host->guest:22 port mapping so the
	// PORTS column reflects how an operator reaches the VM. This is the live
	// truth recorded by VmDeployTarget on first apply.
	if state.SshPort > 0 {
		cs.Ports = append(cs.Ports, PortMapping{
			HostPort: state.SshPort,
			CtrPort:  22,
			Proto:    "tcp",
		})
	}
}

// vmStatusFromDomainState normalises libvirt domain-state vocabulary (as
// rendered by domainStateString) to the unified `ov status` status vocabulary
// shared with the pod substrate (statusFromState). running/paused pass through;
// every powered-off / transitional libvirt state collapses to a single
// "stopped" or its closest unified equivalent.
func vmStatusFromDomainState(state string) string {
	switch state {
	case "running":
		return "running"
	case "paused", "suspended":
		return "paused"
	case "crashed":
		return "dead"
	case "shut off", "shutting down", "":
		return "stopped"
	default:
		return "stopped"
	}
}
