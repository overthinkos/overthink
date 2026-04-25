package main

// `ov test k8s <method>` — Kubernetes cluster probe verbs.
//
// Sibling of libvirt / spice / vnc / cdp / wl / dbus / mcp / record under
// `ov test`. Hermetic: speaks the Kubernetes API directly via the minimal
// client-go subset (clientcmd + dynamic + apimachinery). No dependency on
// an external kubectl on PATH. Cluster selection via --cluster <profile>
// (ClusterProfile in ov/k8s_config.go), --context <ctx>, or --kubeconfig
// <path>. Output is line-oriented so declarative `k8s:` checks in layer
// YAMLs can match against it with the existing stdout/contains/equals
// matchers.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "gopkg.in/yaml.v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ---------------------------------------------------------------------------
// K8sCmd — the Kong command tree mounted at `ov test k8s`.
// ---------------------------------------------------------------------------

type K8sCmd struct {
	Nodes        K8sNodesCmd        `cmd:"" help:"List cluster nodes (name + Ready status per line)"`
	WaitNodes    K8sWaitNodesCmd    `cmd:"" name:"wait-nodes" help:"Block until N nodes are Ready (or a named node is Ready)"`
	Pods         K8sPodsCmd         `cmd:"" help:"List pods (optionally scoped by namespace and/or label selector)"`
	WaitReady    K8sWaitReadyCmd    `cmd:"" name:"wait-ready" help:"Block until a resource reaches its Ready condition"`
	Ingress      K8sIngressCmd      `cmd:"" help:"List ingresses (class / host / backend per line)"`
	IngressClass K8sIngressClassCmd `cmd:"" name:"ingressclass" help:"List ingress classes (name + default flag per line)"`
	StorageClass K8sStorageClassCmd `cmd:"" name:"storageclass" help:"List storage classes (name + default flag per line)"`
	Service      K8sServiceCmd      `cmd:"" help:"List services (ns/name type clusterIP externalIP per line)"`
	LbExternalIP K8sLbExternalIPCmd `cmd:"" name:"lb-external-ip" help:"Print the ServiceLB-assigned external IP for a LoadBalancer service"`
	Addons       K8sAddonsCmd       `cmd:"" help:"Roll-up health check: Traefik + ServiceLB + local-path-provisioner all Ready"`
	Apply        K8sApplyCmd        `cmd:"" help:"Apply a manifest file (YAML, possibly multi-doc) via server-side dynamic client"`
	Delete       K8sDeleteCmd       `cmd:"" help:"Delete resources declared in a manifest file (mirror of apply)"`
	Raw          K8sRawCmd          `cmd:"" help:"GET/list/describe an arbitrary resource by kind/name/namespace"`
}

// ---------------------------------------------------------------------------
// Shared flags for every K8s subcommand.
// ---------------------------------------------------------------------------

// k8sClusterFlags carries the cluster-selection flags. Resolution priority:
//  1. --kubeconfig <path> overrides everything (raw file pointer).
//  2. --cluster <name> loads a ClusterProfile — its KubeconfigContext
//     names the kubeconfig context to activate. Kubeconfig path defaults
//     to $KUBECONFIG then ~/.kube/config.
//  3. --context <ctx> overrides the context from step 2 (or selects one
//     in the default kubeconfig when --cluster is omitted).
//  4. If nothing is given, the current-context of the default kubeconfig
//     is used (same behavior as kubectl with no flags).
type k8sClusterFlags struct {
	Cluster    string `long:"cluster" help:"ClusterProfile name (looked up via LoadClusterProfile)"`
	Context    string `long:"context" help:"kubeconfig context name — overrides cluster profile context"`
	Kubeconfig string `long:"kubeconfig" env:"KUBECONFIG" help:"Path to kubeconfig file (defaults to ~/.kube/config)"`
}

func (f *k8sClusterFlags) restConfig() (*rest.Config, error) {
	var ctxName string
	if f.Cluster != "" {
		// Schema v4: cluster profiles absorbed into kind:k8s entities.
		// Lookup unified loader for the matching K8sSpec's context.
		cwd, _ := os.Getwd()
		if spec := findK8sSpec(cwd, f.Cluster); spec != nil {
			ctxName = spec.KubeconfigContext
		}
	}
	if f.Context != "" {
		ctxName = f.Context
	}

	kubeconfigPath := f.Kubeconfig
	if kubeconfigPath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loadingRules.ExplicitPath = kubeconfigPath
	}
	overrides := &clientcmd.ConfigOverrides{}
	if ctxName != "" {
		overrides.CurrentContext = ctxName
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
}

func (f *k8sClusterFlags) dynamicClient() (dynamic.Interface, error) {
	cfg, err := f.restConfig()
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(cfg)
}

// ---------------------------------------------------------------------------
// Shared helpers.
// ---------------------------------------------------------------------------

// Canonical GVRs used by the probe methods. Listed here so adding a new
// resource kind to probe is a one-line addition, and so the dynamic-client
// calls below stay legible.
var (
	gvrNodes        = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	gvrPods         = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	gvrServices     = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	gvrIngresses    = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	gvrIngressClass = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses"}
	gvrStorageClass = schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}
	gvrDeployments  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	gvrDaemonSets   = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	gvrStatefulSets = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
)

// parseTimeout interprets a Kong-supplied duration string like "120s" or
// "5m". Zero / empty defaults to 60s. Used by every wait-* subcommand.
func parseTimeout(s string) time.Duration {
	if s == "" {
		return 60 * time.Second
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 60 * time.Second
	}
	return d
}

// nodeReady returns true when the given Node has Ready=True among its
// conditions. Uses unstructured access since we intentionally do not
// import the typed v1.Node clientset (binary-size cost).
func nodeReady(u *unstructured.Unstructured) bool {
	conds, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, raw := range conds {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if m["type"] == "Ready" && m["status"] == "True" {
			return true
		}
	}
	return false
}

// workloadReady checks whether a Deployment / DaemonSet / StatefulSet has
// its readyReplicas / numberReady meet its expected count.
func workloadReady(u *unstructured.Unstructured) bool {
	switch u.GetKind() {
	case "Deployment", "StatefulSet":
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "readyReplicas")
		want, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
		if want == 0 {
			return ready > 0
		}
		return ready >= want
	case "DaemonSet":
		ready, _, _ := unstructured.NestedInt64(u.Object, "status", "numberReady")
		want, _, _ := unstructured.NestedInt64(u.Object, "status", "desiredNumberScheduled")
		if want == 0 {
			return ready > 0
		}
		return ready >= want
	case "Pod":
		phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
		return phase == "Running" || phase == "Succeeded"
	}
	return false
}

// ---------------------------------------------------------------------------
// nodes
// ---------------------------------------------------------------------------

type K8sNodesCmd struct {
	k8sClusterFlags
}

func (c *K8sNodesCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	list, err := client.Resource(gvrNodes).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing nodes: %w", err)
	}
	for _, n := range list.Items {
		state := "NotReady"
		if nodeReady(&n) {
			state = "Ready"
		}
		fmt.Printf("%s %s\n", n.GetName(), state)
	}
	return nil
}

// ---------------------------------------------------------------------------
// wait-nodes
// ---------------------------------------------------------------------------

type K8sWaitNodesCmd struct {
	Count   int    `long:"count" default:"1" help:"Number of nodes that must be Ready (ignored if --name is set)"`
	Name    string `long:"name" help:"Wait for a specific node by name instead of a Ready count"`
	Timeout string `long:"timeout" default:"120s" help:"Overall wait timeout (e.g., 120s, 5m)"`
	k8sClusterFlags
}

func (c *K8sWaitNodesCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(parseTimeout(c.Timeout))
	for {
		list, err := client.Resource(gvrNodes).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("listing nodes: %w", err)
		}
		if c.Name != "" {
			for _, n := range list.Items {
				if n.GetName() == c.Name && nodeReady(&n) {
					fmt.Printf("%s Ready\n", c.Name)
					return nil
				}
			}
		} else {
			ready := 0
			var names []string
			for _, n := range list.Items {
				if nodeReady(&n) {
					ready++
					names = append(names, n.GetName())
				}
			}
			if ready >= c.Count {
				for _, n := range names {
					fmt.Printf("%s Ready\n", n)
				}
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for nodes Ready (want count=%d name=%q)", c.Count, c.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// pods
// ---------------------------------------------------------------------------

type K8sPodsCmd struct {
	Namespace string `long:"namespace" short:"n" help:"Namespace (empty = all namespaces)"`
	Label     string `long:"label" help:"Label selector (e.g., app=traefik)"`
	k8sClusterFlags
}

func (c *K8sPodsCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	opts := metav1.ListOptions{LabelSelector: c.Label}
	var list *unstructured.UnstructuredList
	if c.Namespace == "" {
		list, err = client.Resource(gvrPods).List(context.TODO(), opts)
	} else {
		list, err = client.Resource(gvrPods).Namespace(c.Namespace).List(context.TODO(), opts)
	}
	if err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}
	for _, p := range list.Items {
		phase, _, _ := unstructured.NestedString(p.Object, "status", "phase")
		fmt.Printf("%s/%s %s\n", p.GetNamespace(), p.GetName(), phase)
	}
	return nil
}

// ---------------------------------------------------------------------------
// wait-ready
// ---------------------------------------------------------------------------

type K8sWaitReadyCmd struct {
	Kind      string `long:"kind" required:"" help:"Resource kind (Deployment, DaemonSet, StatefulSet, Pod)"`
	Name      string `long:"name" required:"" help:"Resource name"`
	Namespace string `long:"namespace" short:"n" default:"default" help:"Namespace"`
	Timeout   string `long:"timeout" default:"120s" help:"Overall wait timeout"`
	k8sClusterFlags
}

func (c *K8sWaitReadyCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	gvr, ok := kindToGVR(c.Kind)
	if !ok {
		return fmt.Errorf("unsupported kind %q (want Deployment, DaemonSet, StatefulSet, or Pod)", c.Kind)
	}
	deadline := time.Now().Add(parseTimeout(c.Timeout))
	for {
		u, err := client.Resource(gvr).Namespace(c.Namespace).Get(context.TODO(), c.Name, metav1.GetOptions{})
		if err == nil && workloadReady(u) {
			fmt.Printf("%s/%s Ready\n", c.Namespace, c.Name)
			return nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting %s/%s: %w", c.Namespace, c.Name, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s/%s/%s Ready", c.Kind, c.Namespace, c.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

func kindToGVR(kind string) (schema.GroupVersionResource, bool) {
	switch strings.ToLower(kind) {
	case "deployment", "deploy":
		return gvrDeployments, true
	case "daemonset", "ds":
		return gvrDaemonSets, true
	case "statefulset", "sts":
		return gvrStatefulSets, true
	case "pod":
		return gvrPods, true
	}
	return schema.GroupVersionResource{}, false
}

// ---------------------------------------------------------------------------
// ingress
// ---------------------------------------------------------------------------

type K8sIngressCmd struct {
	Namespace string `long:"namespace" short:"n" help:"Namespace (empty = all)"`
	k8sClusterFlags
}

func (c *K8sIngressCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	var list *unstructured.UnstructuredList
	opts := metav1.ListOptions{}
	if c.Namespace == "" {
		list, err = client.Resource(gvrIngresses).List(context.TODO(), opts)
	} else {
		list, err = client.Resource(gvrIngresses).Namespace(c.Namespace).List(context.TODO(), opts)
	}
	if err != nil {
		return fmt.Errorf("listing ingresses: %w", err)
	}
	for _, ing := range list.Items {
		class, _, _ := unstructured.NestedString(ing.Object, "spec", "ingressClassName")
		hosts := ingressHosts(&ing)
		backends := ingressBackends(&ing)
		fmt.Printf("%s/%s class=%s hosts=%s backends=%s\n",
			ing.GetNamespace(), ing.GetName(), class,
			strings.Join(hosts, ","), strings.Join(backends, ","))
	}
	return nil
}

func ingressHosts(u *unstructured.Unstructured) []string {
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	var out []string
	for _, r := range rules {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		if h, ok := m["host"].(string); ok && h != "" {
			out = append(out, h)
		}
	}
	return out
}

func ingressBackends(u *unstructured.Unstructured) []string {
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	var out []string
	for _, r := range rules {
		m, _ := r.(map[string]interface{})
		http, _ := m["http"].(map[string]interface{})
		paths, _ := http["paths"].([]interface{})
		for _, p := range paths {
			pm, _ := p.(map[string]interface{})
			backend, _ := pm["backend"].(map[string]interface{})
			svc, _ := backend["service"].(map[string]interface{})
			if name, ok := svc["name"].(string); ok {
				out = append(out, name)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// ingressclass
// ---------------------------------------------------------------------------

type K8sIngressClassCmd struct {
	k8sClusterFlags
}

func (c *K8sIngressClassCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	list, err := client.Resource(gvrIngressClass).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing ingress classes: %w", err)
	}
	for _, ic := range list.Items {
		def := ic.GetAnnotations()["ingressclass.kubernetes.io/is-default-class"]
		isDefault := def == "true"
		fmt.Printf("%s default=%t\n", ic.GetName(), isDefault)
	}
	return nil
}

// ---------------------------------------------------------------------------
// storageclass
// ---------------------------------------------------------------------------

type K8sStorageClassCmd struct {
	k8sClusterFlags
}

func (c *K8sStorageClassCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	list, err := client.Resource(gvrStorageClass).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing storage classes: %w", err)
	}
	for _, sc := range list.Items {
		def := sc.GetAnnotations()["storageclass.kubernetes.io/is-default-class"]
		isDefault := def == "true"
		fmt.Printf("%s default=%t\n", sc.GetName(), isDefault)
	}
	return nil
}

// ---------------------------------------------------------------------------
// service
// ---------------------------------------------------------------------------

type K8sServiceCmd struct {
	Namespace string `long:"namespace" short:"n" help:"Namespace (empty = all)"`
	k8sClusterFlags
}

func (c *K8sServiceCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	var list *unstructured.UnstructuredList
	opts := metav1.ListOptions{}
	if c.Namespace == "" {
		list, err = client.Resource(gvrServices).List(context.TODO(), opts)
	} else {
		list, err = client.Resource(gvrServices).Namespace(c.Namespace).List(context.TODO(), opts)
	}
	if err != nil {
		return fmt.Errorf("listing services: %w", err)
	}
	for _, s := range list.Items {
		svcType, _, _ := unstructured.NestedString(s.Object, "spec", "type")
		clusterIP, _, _ := unstructured.NestedString(s.Object, "spec", "clusterIP")
		externalIP := serviceExternalIP(&s)
		fmt.Printf("%s/%s %s %s %s\n",
			s.GetNamespace(), s.GetName(), svcType, clusterIP, externalIP)
	}
	return nil
}

func serviceExternalIP(u *unstructured.Unstructured) string {
	ingress, _, _ := unstructured.NestedSlice(u.Object, "status", "loadBalancer", "ingress")
	for _, raw := range ingress {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if ip, ok := m["ip"].(string); ok && ip != "" {
			return ip
		}
		if host, ok := m["hostname"].(string); ok && host != "" {
			return host
		}
	}
	// Fall back to externalIPs on spec.
	ips, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "externalIPs")
	if len(ips) > 0 {
		return ips[0]
	}
	return "<none>"
}

// ---------------------------------------------------------------------------
// lb-external-ip
// ---------------------------------------------------------------------------

type K8sLbExternalIPCmd struct {
	Namespace string `long:"namespace" short:"n" required:"" help:"Namespace of the LoadBalancer service"`
	Name      string `long:"name" required:"" help:"Service name"`
	Timeout   string `long:"timeout" default:"60s" help:"Timeout waiting for assignment"`
	k8sClusterFlags
}

func (c *K8sLbExternalIPCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(parseTimeout(c.Timeout))
	for {
		svc, err := client.Resource(gvrServices).Namespace(c.Namespace).Get(context.TODO(), c.Name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting service: %w", err)
		}
		if err == nil {
			ip := serviceExternalIP(svc)
			if ip != "<none>" {
				fmt.Println(ip)
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout: no external IP assigned to %s/%s", c.Namespace, c.Name)
		}
		time.Sleep(2 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// addons — roll-up of the k3s default addon stack
// ---------------------------------------------------------------------------

type K8sAddonsCmd struct {
	Namespace string `long:"namespace" short:"n" default:"kube-system" help:"Namespace the addons live in (default: kube-system)"`
	Timeout   string `long:"timeout" default:"180s" help:"Overall wait timeout"`
	k8sClusterFlags
}

// k3sAddons is the fixed list of addon workloads the `addons` method waits
// on. Names match the stock k3s deployment; if the operator explicitly
// disables one via `disable:` in k3s config, this check fails fast (which
// is the intended signal — a k3s-server layer test expects them all).
var k3sAddons = []struct {
	Kind string
	Name string
}{
	{"Deployment", "traefik"},
	{"Deployment", "local-path-provisioner"},
	{"DaemonSet", "svclb-traefik"},
}

func (c *K8sAddonsCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(parseTimeout(c.Timeout))
	for _, addon := range k3sAddons {
		gvr, _ := kindToGVR(addon.Kind)
		if err := waitWorkloadReady(client, gvr, c.Namespace, addon.Name, deadline); err != nil {
			return err
		}
		fmt.Printf("%s/%s/%s Ready\n", addon.Kind, c.Namespace, addon.Name)
	}
	// svclb-traefik is a DaemonSet that carries a suffix on some k3s versions
	// (svclb-traefik-<hash>); if the literal name wasn't found we fall back to
	// a prefix scan so the check is resilient to k3s naming churn.
	return nil
}

func waitWorkloadReady(client dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, deadline time.Time) error {
	for {
		u, err := client.Resource(gvr).Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err == nil && workloadReady(u) {
			return nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("get %s/%s: %w", namespace, name, err)
		}
		// For svclb-traefik, fall back to prefix match on the DaemonSet list.
		if err != nil && apierrors.IsNotFound(err) && strings.HasPrefix(name, "svclb-") {
			list, lerr := client.Resource(gvr).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
			if lerr == nil {
				for _, it := range list.Items {
					if strings.HasPrefix(it.GetName(), name) && workloadReady(&it) {
						return nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s/%s Ready (last err: %v)", namespace, name, err)
		}
		time.Sleep(3 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// apply
// ---------------------------------------------------------------------------

type K8sApplyCmd struct {
	File      string `long:"file" short:"f" required:"" help:"Path to YAML manifest (may contain multiple documents)"`
	Namespace string `long:"namespace" short:"n" help:"Namespace override for resources without metadata.namespace"`
	k8sClusterFlags
}

func (c *K8sApplyCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	docs, err := readYamlDocs(c.File)
	if err != nil {
		return err
	}
	for _, u := range docs {
		ns := u.GetNamespace()
		if ns == "" && c.Namespace != "" {
			u.SetNamespace(c.Namespace)
			ns = c.Namespace
		}
		gvr, err := gvrForObject(u)
		if err != nil {
			return err
		}
		var iface dynamic.ResourceInterface = client.Resource(gvr)
		if ns != "" {
			iface = client.Resource(gvr).Namespace(ns)
		}
		_, err = iface.Create(context.TODO(), u, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			existing, gerr := iface.Get(context.TODO(), u.GetName(), metav1.GetOptions{})
			if gerr != nil {
				return fmt.Errorf("get existing %s/%s: %w", ns, u.GetName(), gerr)
			}
			u.SetResourceVersion(existing.GetResourceVersion())
			_, err = iface.Update(context.TODO(), u, metav1.UpdateOptions{})
		}
		if err != nil {
			return fmt.Errorf("apply %s/%s: %w", ns, u.GetName(), err)
		}
		fmt.Printf("applied %s %s/%s\n", u.GetKind(), ns, u.GetName())
	}
	return nil
}

// ---------------------------------------------------------------------------
// delete
// ---------------------------------------------------------------------------

type K8sDeleteCmd struct {
	File      string `long:"file" short:"f" required:"" help:"Path to YAML manifest — resources listed here will be deleted"`
	Namespace string `long:"namespace" short:"n" help:"Namespace override for resources without metadata.namespace"`
	k8sClusterFlags
}

func (c *K8sDeleteCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	docs, err := readYamlDocs(c.File)
	if err != nil {
		return err
	}
	for _, u := range docs {
		ns := u.GetNamespace()
		if ns == "" && c.Namespace != "" {
			ns = c.Namespace
		}
		gvr, err := gvrForObject(u)
		if err != nil {
			return err
		}
		var iface dynamic.ResourceInterface = client.Resource(gvr)
		if ns != "" {
			iface = client.Resource(gvr).Namespace(ns)
		}
		err = iface.Delete(context.TODO(), u.GetName(), metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			fmt.Printf("skip %s %s/%s (not found)\n", u.GetKind(), ns, u.GetName())
			continue
		}
		if err != nil {
			return fmt.Errorf("delete %s/%s: %w", ns, u.GetName(), err)
		}
		fmt.Printf("deleted %s %s/%s\n", u.GetKind(), ns, u.GetName())
	}
	return nil
}

// ---------------------------------------------------------------------------
// raw
// ---------------------------------------------------------------------------

type K8sRawCmd struct {
	Group     string `long:"group" default:"" help:"API group (empty for core: pods, services, nodes, etc.)"`
	Version   string `long:"version" default:"v1" help:"API version"`
	Resource  string `long:"resource" required:"" help:"Plural resource name (e.g., pods, deployments, ingresses)"`
	Name      string `long:"name" help:"Specific resource name (empty = list)"`
	Namespace string `long:"namespace" short:"n" help:"Namespace (empty = cluster-scoped or all-namespaces)"`
	k8sClusterFlags
}

func (c *K8sRawCmd) Run() error {
	client, err := c.dynamicClient()
	if err != nil {
		return err
	}
	gvr := schema.GroupVersionResource{Group: c.Group, Version: c.Version, Resource: c.Resource}
	var iface dynamic.ResourceInterface = client.Resource(gvr)
	if c.Namespace != "" {
		iface = client.Resource(gvr).Namespace(c.Namespace)
	}
	if c.Name != "" {
		u, err := iface.Get(context.TODO(), c.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get %s/%s: %w", c.Resource, c.Name, err)
		}
		return writeUnstructuredJSON(os.Stdout, u)
	}
	list, err := iface.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list %s: %w", c.Resource, err)
	}
	// Sort for stable output.
	sort.Slice(list.Items, func(i, j int) bool {
		if list.Items[i].GetNamespace() != list.Items[j].GetNamespace() {
			return list.Items[i].GetNamespace() < list.Items[j].GetNamespace()
		}
		return list.Items[i].GetName() < list.Items[j].GetName()
	})
	for _, u := range list.Items {
		if u.GetNamespace() != "" {
			fmt.Printf("%s/%s\n", u.GetNamespace(), u.GetName())
		} else {
			fmt.Println(u.GetName())
		}
	}
	return nil
}

func writeUnstructuredJSON(w io.Writer, u *unstructured.Unstructured) error {
	return writeJSON(w, u.Object)
}

// ---------------------------------------------------------------------------
// YAML ingestion helpers (shared by apply/delete).
// ---------------------------------------------------------------------------

// readYamlDocs reads a YAML file that may contain multiple documents
// separated by `---` and decodes each into an Unstructured.
func readYamlDocs(path string) ([]*unstructured.Unstructured, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var out []*unstructured.Unstructured
	decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 4096)
	for {
		raw := map[string]interface{}{}
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding %s: %w", path, err)
		}
		if len(raw) == 0 {
			continue
		}
		u := &unstructured.Unstructured{Object: raw}
		out = append(out, u)
	}
	return out, nil
}

// gvrForObject maps an Unstructured to its GroupVersionResource. For the
// probe-scope we ship, a static kind→GVR table is sufficient and avoids
// depending on a discovery RESTMapper (another chunk of binary bloat).
func gvrForObject(u *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	gv, err := schema.ParseGroupVersion(u.GetAPIVersion())
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("parsing apiVersion %q: %w", u.GetAPIVersion(), err)
	}
	resource, ok := kindToPluralResource(u.GetKind())
	if !ok {
		return schema.GroupVersionResource{}, fmt.Errorf("unsupported kind %q for apply/delete (add mapping in kindToPluralResource)", u.GetKind())
	}
	return schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: resource}, nil
}

// kindToPluralResource maps the Kind field of a manifest document to its
// pluralized REST resource name. Kept intentionally narrow — the apply /
// delete surface is meant for smoke-test manifests authored for the k3s
// verification flow, not production charts.
func kindToPluralResource(kind string) (string, bool) {
	switch kind {
	case "Pod":
		return "pods", true
	case "Service":
		return "services", true
	case "Deployment":
		return "deployments", true
	case "DaemonSet":
		return "daemonsets", true
	case "StatefulSet":
		return "statefulsets", true
	case "Ingress":
		return "ingresses", true
	case "ConfigMap":
		return "configmaps", true
	case "Secret":
		return "secrets", true
	case "Namespace":
		return "namespaces", true
	case "ServiceAccount":
		return "serviceaccounts", true
	case "Job":
		return "jobs", true
	case "CronJob":
		return "cronjobs", true
	case "PersistentVolumeClaim":
		return "persistentvolumeclaims", true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Schema v4: WriteClusterProfile removed.
//
// The user-scoped auto-write to ~/.config/ov/clusters/<name>.yaml is gone.
// k3s-provisioned clusters get a kind:k8s entity authored inline (via
// `ov migrate schema-v4` for existing profiles, or directly in
// overthink.yml / k8s.yml for new deployments).
//
// A stub is kept below so k3s_post.go compiles; in v4, the k3s-server
// layer's artifacts: block is what surfaces the kubeconfig, and operators
// author the matching kind:k8s entry themselves.
// ---------------------------------------------------------------------------

// WriteClusterProfile is a no-op in schema v4. Kept as a stub so callers
// compile during the cutover. Remove all call sites before shipping.
func WriteClusterProfile(name, contextName string) error {
	// Intentionally a no-op. See comment block above.
	_ = name
	_ = contextName
	return nil
}

// findK8sSpec looks up a K8sSpec by name from the project's overthink.yml /
// k8s.yml via the unified loader. Returns nil if no matching kind:k8s
// entity exists or if the unified file can't be loaded.
func findK8sSpec(dir, name string) *K8sSpec {
	if dir == "" || name == "" {
		return nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return nil
	}
	if uf.K8s == nil {
		return nil
	}
	return uf.K8s[name]
}

// findPodSpec looks up a PodSpec by name from the unified loader.
func findPodSpec(dir, name string) *PodSpec {
	if dir == "" || name == "" {
		return nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return nil
	}
	if uf.Pod == nil {
		return nil
	}
	return uf.Pod[name]
}

// findHostSpec looks up a HostSpec by name from the unified loader.
func findHostSpec(dir, name string) *HostSpec {
	if dir == "" || name == "" {
		return nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return nil
	}
	if uf.Host == nil {
		return nil
	}
	return uf.Host[name]
}

// Force-use the strconv import so the file compiles even if none of the
// methods above happen to reach a code path that touches it. The Kong
// default-value parser uses string-to-int parsing for several `default:`
// tags, but some Go versions warn on unused imports from vendored subsets.
var _ = strconv.Itoa
