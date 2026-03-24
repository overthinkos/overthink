package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SecretYAML represents a secret declaration in layer.yml.
type SecretYAML struct {
	Name   string `yaml:"name"`             // unique secret name
	Target string `yaml:"target,omitempty"` // container mount path (default: /run/secrets/<name>)
	Env    string `yaml:"env,omitempty"`     // fallback env var name
}

// LabelSecret represents a secret requirement in an OCI image label.
// Only metadata is stored — never the secret value.
type LabelSecret struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Env    string `json:"env,omitempty"`
}

// CollectedSecret represents a fully resolved secret ready for provisioning.
type CollectedSecret struct {
	Name       string // podman secret name: "ov-<image>-<name>"
	Target     string // container mount path
	Env        string // fallback env var name
	SecretName string // original secret name from layer.yml
}

// CollectSecretsFromLabels reconstructs secrets from image label metadata.
func CollectSecretsFromLabels(imageName string, labelSecrets []LabelSecret) []CollectedSecret {
	var secrets []CollectedSecret
	for _, ls := range labelSecrets {
		secrets = append(secrets, CollectedSecret{
			Name:       "ov-" + imageName + "-" + ls.Name,
			Target:     ls.Target,
			Env:        ls.Env,
			SecretName: ls.Name,
		})
	}
	return secrets
}

// ProvisionPodmanSecrets creates podman secrets from the credential store.
// Returns the secrets that were successfully provisioned and any that fell back to env vars.
func ProvisionPodmanSecrets(engine, imageName, instance string, secrets []CollectedSecret) (provisioned []CollectedSecret, fallbackEnv []string, err error) {
	if engine == "docker" {
		fmt.Fprintln(os.Stderr, "NOTE: Docker secrets require Swarm mode (not available).")
		fmt.Fprintln(os.Stderr, "Falling back to environment variable injection for secrets.")
		fmt.Fprintln(os.Stderr, "This is less secure — secret values will be visible in 'docker inspect'.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Consider using Podman for better secrets support:")
		fmt.Fprintln(os.Stderr, "  ov config set engine.run podman")
		// Fall back to env vars for all secrets
		for _, s := range secrets {
			if s.Env != "" {
				val, _ := resolveSecretValue(s, imageName, instance)
				if val != "" {
					fallbackEnv = append(fallbackEnv, s.Env+"="+val)
				}
			}
		}
		return nil, fallbackEnv, nil
	}

	if len(secrets) > 0 {
		fmt.Fprintln(os.Stderr, "Provisioning container secrets:")
	}
	for _, s := range secrets {
		val, source := resolveSecretValue(s, imageName, instance)
		if val == "" {
			fmt.Fprintf(os.Stderr, "  %-40s → no value configured\n", s.Name)
			fmt.Fprintf(os.Stderr, "\n")
			fmt.Fprintf(os.Stderr, "WARNING: Secret '%s' has no value configured.\n", s.SecretName)
			fmt.Fprintf(os.Stderr, "The container may fail to start properly.\n\n")
			fmt.Fprintf(os.Stderr, "To set it:\n")
			if s.Env != "" {
				fmt.Fprintf(os.Stderr, "  %s=xxx ov enable %s  (env var override)\n", s.Env, imageName)
			}
			fmt.Fprintf(os.Stderr, "  ov config set <credential-key> YOUR_VALUE\n\n")
			continue
		}

		if err := ensurePodmanSecret(engine, s.Name, val); err != nil {
			fmt.Fprintf(os.Stderr, "  %-40s → FAILED: %v\n", s.Name, err)
			// Fall back to env var if available
			if s.Env != "" {
				fallbackEnv = append(fallbackEnv, s.Env+"="+val)
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-40s → created (from %s)\n", s.Name, source)
		provisioned = append(provisioned, s)
	}

	if len(provisioned) > 0 {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Note: Secrets are mounted at /run/secrets/<name> inside the container.")
		fmt.Fprintf(os.Stderr, "To update a secret after changing it: ov update %s\n", imageName)
	}

	return provisioned, fallbackEnv, nil
}

// SecretArgs returns --secret flags for container run (direct mode).
func SecretArgs(secrets []CollectedSecret) []string {
	var args []string
	for _, s := range secrets {
		args = append(args, "--secret", fmt.Sprintf("%s,target=%s", s.Name, s.Target))
	}
	return args
}

// resolveSecretValue looks up the value for a secret from the credential store.
func resolveSecretValue(s CollectedSecret, imageName, instance string) (value, source string) {
	// If the secret has an associated env var, check it first
	if s.Env != "" {
		val, src := ResolveCredential(s.Env, credServiceForSecret(s.Env), credKeyForSecret(imageName, instance), "")
		if val != "" {
			return val, src
		}
	}
	// Generic lookup by secret name
	val, src := ResolveCredential("", "ov/secret", s.SecretName, "")
	return val, src
}

// credServiceForSecret maps well-known env vars to credential services.
func credServiceForSecret(envVar string) string {
	switch envVar {
	case "VNC_PASSWORD":
		return CredServiceVNC
	case "SUNSHINE_USER":
		return CredServiceSunshineUser
	case "SUNSHINE_PASSWORD":
		return CredServiceSunshinePassword
	default:
		return "ov/secret"
	}
}

// credKeyForSecret returns the credential key for an image/instance pair.
func credKeyForSecret(imageName, instance string) string {
	if instance != "" {
		return imageName + "-" + instance
	}
	return imageName
}

// ensurePodmanSecret creates or replaces a podman secret.
func ensurePodmanSecret(engine, name, value string) error {
	binary := EngineBinary(engine)
	// Remove existing secret (ignore error if doesn't exist)
	rmCmd := exec.Command(binary, "secret", "rm", name)
	rmCmd.Stderr = nil
	_ = rmCmd.Run()

	// Create new secret from stdin
	createCmd := exec.Command(binary, "secret", "create", name, "-")
	createCmd.Stdin = strings.NewReader(value)
	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("podman secret create %s: %w\n%s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// RemovePodmanSecrets removes podman secrets for an image (best-effort).
func RemovePodmanSecrets(engine string, secrets []CollectedSecret) {
	binary := EngineBinary(engine)
	for _, s := range secrets {
		cmd := exec.Command(binary, "secret", "rm", s.Name)
		cmd.Stderr = nil
		_ = cmd.Run()
	}
}
