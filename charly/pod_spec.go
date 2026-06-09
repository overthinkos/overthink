package main

// PodSpec is a minimal target template for pod deployments. It names a
// kind:image and optionally adds genuinely pod-specific extras the image
// can't declare (sidecars, target-specific tests, shared env-value
// defaults across multiple deployments of the same image).
//
// Declaration fields (ports, env, network, security, init, env_file,
// services) DO NOT appear here — they are image declarations emitted as
// OCI labels (see charly/generate.go:writeLabels + charly/labels.go). A kind:pod
// template MUST NOT shadow them. A deployment overrides image declarations
// via MergeDeployOntoMetadata (charly/deploy.go:630-737).
//
// Most projects will have no kind:pod entries; the common case is a
// kind:deployment with target:pod referencing an image directly via
// `image: <name>`. A kind:pod entry exists only when the operator needs
// to add pod-specific extras that are reused across multiple deployments.
type PodSpec struct {
	// Image is the kind:image name this pod template wraps. Required.
	Image string `yaml:"box"`

	// Sidecars are additional containers that accompany the main pod
	// container. Genuinely pod-specific: only meaningful at pod deployment
	// time, not declared by the image itself.
	Sidecar []SidecarConfig `yaml:"sidecar,omitempty"`

	// Secret entries are secret requirements that apply to any deployment
	// using this template. Same type as DeploymentNode.Secret — deployment
	// entries can add additional secrets on top.
	Secret []DeploySecretConfig `yaml:"secret,omitempty"`

	// EnvDefaults supplies default VALUES for env vars the IMAGE already
	// declares via LabelEnv. Use only when multiple deployments share the
	// same default (e.g., WEBUI_ADMIN_EMAIL=admin@example.com). The image
	// declares WHICH env vars exist; this field provides OPTIONAL default
	// values. Deployment overrides any of them.
	EnvDefaults []string `yaml:"env_default,omitempty"`

	// Tests are optional target-specific build-scope checks that run for
	// every deployment using this template. Default empty — layer tests
	// and image tests stay where authored and propagate automatically.
	Eval []Check `yaml:"eval,omitempty"`

	// DeployTests are optional target-specific deploy-scope defaults.
	// Default empty. Deployment-level tests overlay on top via
	// MergeDeployEval.
	DeployEval []Check `yaml:"deploy_eval,omitempty"`
}
