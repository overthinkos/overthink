package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// helper: read + parse a YAML file into a map for assertion
func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// fixtureCluster returns a representative production-style cluster profile.
func fixtureCluster(name string) *ClusterProfile {
	return &ClusterProfile{
		Version:           1,
		Kind:              "cluster-profile",
		Name:              name,
		KubeconfigContext: "gke_prod",
		AdmissionPolicy:   "restricted",
		DefaultNamespace:  "apps",
		Storage: ClusterStorage{
			ClassDefault:      "standard",
			ClassFast:         "fast-ssd",
			ClassEncrypted:    "fast-ssd-luks",
			AccessModeDefault: "ReadWriteOnce",
		},
		Ingress: ClusterIngress{
			Enabled:         true,
			Class:           "nginx",
			CertIssuer:      "letsencrypt-prod",
			PathTypeDefault: "Prefix",
		},
		Images: ClusterImages{PullPolicy: "IfNotPresent"},
		Defaults: ClusterResourceDefaults{
			Labels: map[string]string{"managed-by": "overthink"},
		},
	}
}

func TestK8sGenerate_DeploymentBasics(t *testing.T) {
	out := t.TempDir()
	opts := K8sGenerateOpts{
		DeploymentName: "openclaw",
		ImageRef:       "quay.io/example/openclaw:v1",
		Capabilities: &Capabilities{
			Image: "openclaw",
			UID:   1000,
			GID:   1000,
			Ports: []string{"8080"},
		},
		Cluster:   fixtureCluster("prod"),
		OutputDir: out,
	}
	overlayDir, err := GenerateK8sKustomize(opts)
	if err != nil {
		t.Fatalf("GenerateK8sKustomize: %v", err)
	}

	baseDir := filepath.Join(out, "openclaw", "base")
	deploy := readYAML(t, filepath.Join(baseDir, "deployment.yaml"))
	if deploy["kind"] != "Deployment" {
		t.Errorf("kind = %v, want Deployment", deploy["kind"])
	}
	meta := deploy["metadata"].(map[string]any)
	if meta["name"] != "openclaw" {
		t.Errorf("metadata.name = %v", meta["name"])
	}

	svc := readYAML(t, filepath.Join(baseDir, "service.yaml"))
	if svc["kind"] != "Service" {
		t.Errorf("service kind = %v", svc["kind"])
	}

	bk := readYAML(t, filepath.Join(baseDir, "kustomization.yaml"))
	resources, _ := bk["resources"].([]any)
	if len(resources) < 2 {
		t.Errorf("base kustomization resources = %v, want ≥2 entries", resources)
	}

	// Overlay exists with namespace override
	ok := readYAML(t, filepath.Join(overlayDir, "kustomization.yaml"))
	if ns, _ := ok["namespace"].(string); ns != "apps" {
		t.Errorf("overlay namespace = %q, want apps", ns)
	}
}

func TestK8sGenerate_StatefulSetWhenStorage(t *testing.T) {
	out := t.TempDir()
	opts := K8sGenerateOpts{
		DeploymentName: "pgbase",
		ImageRef:       "quay.io/example/pgbase:v1",
		Capabilities:   &Capabilities{Image: "pgbase", Ports: []string{"5432"}},
		Deployment: DeployImageConfig{
			Kind: "service",
			Storage: []DeployStorage{
				{Name: "data", Size: "20Gi", ClassHint: "fast", Access: "single-writer"},
			},
		},
		Cluster:   fixtureCluster("prod"),
		OutputDir: out,
	}
	_, err := GenerateK8sKustomize(opts)
	if err != nil {
		t.Fatalf("GenerateK8sKustomize: %v", err)
	}

	ss := readYAML(t, filepath.Join(out, "pgbase", "base", "statefulset.yaml"))
	if ss["kind"] != "StatefulSet" {
		t.Errorf("kind = %v, want StatefulSet (storage present → StatefulSet)", ss["kind"])
	}
	spec := ss["spec"].(map[string]any)
	templates, _ := spec["volumeClaimTemplates"].([]any)
	if len(templates) != 1 {
		t.Fatalf("volumeClaimTemplates = %v, want 1 entry", templates)
	}
	tmpl := templates[0].(map[string]any)
	tmetadata := tmpl["metadata"].(map[string]any)
	if tmetadata["name"] != "data" {
		t.Errorf("volumeClaimTemplate.metadata.name = %v", tmetadata["name"])
	}
	tspec := tmpl["spec"].(map[string]any)
	if sc, _ := tspec["storageClassName"].(string); sc != "fast-ssd" {
		t.Errorf("storageClassName = %q, want fast-ssd (class_hint: fast → cluster.storage.class_fast)", sc)
	}
}

func TestK8sGenerate_DaemonSetAndCronJob(t *testing.T) {
	out := t.TempDir()
	for _, tc := range []struct {
		kind, want, file string
	}{
		{"daemon", "DaemonSet", "daemonset.yaml"},
		{"batch", "Job", "job.yaml"},
	} {
		opts := K8sGenerateOpts{
			DeploymentName: tc.kind + "-test",
			ImageRef:       "quay.io/example/x:v1",
			Capabilities:   &Capabilities{},
			Deployment:     DeployImageConfig{Kind: tc.kind},
			Cluster:        fixtureCluster("prod"),
			OutputDir:      out,
		}
		if _, err := GenerateK8sKustomize(opts); err != nil {
			t.Fatalf("%s generate: %v", tc.kind, err)
		}
		doc := readYAML(t, filepath.Join(out, opts.DeploymentName, "base", tc.file))
		if doc["kind"] != tc.want {
			t.Errorf("%s kind = %v, want %s", tc.kind, doc["kind"], tc.want)
		}
	}

	// CronJob needs schedule
	opts := K8sGenerateOpts{
		DeploymentName: "nightly-backup",
		ImageRef:       "quay.io/example/backup:v1",
		Capabilities:   &Capabilities{},
		Deployment:     DeployImageConfig{Kind: "scheduled", Schedule: "0 3 * * *"},
		Cluster:        fixtureCluster("prod"),
		OutputDir:      out,
	}
	if _, err := GenerateK8sKustomize(opts); err != nil {
		t.Fatalf("cronjob generate: %v", err)
	}
	cj := readYAML(t, filepath.Join(out, "nightly-backup", "base", "cronjob.yaml"))
	if cj["kind"] != "CronJob" {
		t.Errorf("CronJob kind = %v", cj["kind"])
	}
	if sched, _ := cj["spec"].(map[string]any)["schedule"].(string); sched != "0 3 * * *" {
		t.Errorf("schedule = %q", sched)
	}
}

func TestK8sGenerate_IngressWhenExposeSet(t *testing.T) {
	out := t.TempDir()
	opts := K8sGenerateOpts{
		DeploymentName: "web",
		ImageRef:       "quay.io/example/web:v1",
		Capabilities:   &Capabilities{Ports: []string{"8080"}},
		Deployment: DeployImageConfig{
			Expose: &DeployExpose{Host: "web.example.com", TLS: true},
		},
		Cluster:   fixtureCluster("prod"),
		OutputDir: out,
	}
	if _, err := GenerateK8sKustomize(opts); err != nil {
		t.Fatalf("generate: %v", err)
	}
	ing := readYAML(t, filepath.Join(out, "web", "base", "ingress.yaml"))
	if ing["kind"] != "Ingress" {
		t.Fatalf("kind = %v", ing["kind"])
	}
	spec := ing["spec"].(map[string]any)
	if spec["ingressClassName"] != "nginx" {
		t.Errorf("ingressClassName = %v", spec["ingressClassName"])
	}
	tls, _ := spec["tls"].([]any)
	if len(tls) != 1 {
		t.Errorf("tls = %v, want 1 entry", tls)
	}
	metaAnno, _ := ing["metadata"].(map[string]any)["annotations"].(map[string]any)
	if ci, _ := metaAnno["cert-manager.io/cluster-issuer"].(string); ci != "letsencrypt-prod" {
		t.Errorf("cluster-issuer annotation = %q", ci)
	}
}

func TestK8sGenerate_InstanceOverlay(t *testing.T) {
	out := t.TempDir()
	opts := K8sGenerateOpts{
		DeploymentName: "openclaw",
		Instance:       "prod",
		ImageRef:       "quay.io/example/openclaw:v1",
		Capabilities:   &Capabilities{Ports: []string{"8080"}},
		Cluster:        fixtureCluster("prod"),
		OutputDir:      out,
	}
	overlayDir, err := GenerateK8sKustomize(opts)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasSuffix(overlayDir, filepath.Join("overlays", "prod")) {
		t.Errorf("overlayDir = %q, want …/overlays/prod", overlayDir)
	}
	if _, err := os.Stat(filepath.Join(overlayDir, "kustomization.yaml")); err != nil {
		t.Errorf("overlay kustomization.yaml missing: %v", err)
	}
}

func TestK8sGenerate_ResourcesFromSecurityAndRequests(t *testing.T) {
	out := t.TempDir()
	opts := K8sGenerateOpts{
		DeploymentName: "svc",
		ImageRef:       "quay.io/example/svc:v1",
		Capabilities:   &Capabilities{Ports: []string{"8080"}},
		Deployment: DeployImageConfig{
			Resources: &DeployResources{CPURequest: "250m", MemoryRequest: "256Mi"},
			Security:  &SecurityConfig{Cpus: "1.5", MemoryMax: "1Gi"},
		},
		Cluster:   fixtureCluster("prod"),
		OutputDir: out,
	}
	if _, err := GenerateK8sKustomize(opts); err != nil {
		t.Fatalf("generate: %v", err)
	}
	deploy := readYAML(t, filepath.Join(out, "svc", "base", "deployment.yaml"))
	spec := deploy["spec"].(map[string]any)
	tmpl := spec["template"].(map[string]any)
	pspec := tmpl["spec"].(map[string]any)
	containers := pspec["containers"].([]any)
	c0 := containers[0].(map[string]any)
	res := c0["resources"].(map[string]any)
	req := res["requests"].(map[string]any)
	if req["cpu"] != "250m" || req["memory"] != "256Mi" {
		t.Errorf("requests = %v", req)
	}
	lim := res["limits"].(map[string]any)
	if lim["cpu"] != "1.5" || lim["memory"] != "1Gi" {
		t.Errorf("limits = %v (expected from Security.Cpus/MemoryMax)", lim)
	}
}
