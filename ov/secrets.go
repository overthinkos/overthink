package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// generateRandomHex generates a cryptographically random hex string of the given byte length.
func generateRandomHex(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

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
//
// Service, Key, and RotateOnConfig are populated by CollectLayerSecretAccepts
// (added in a later step) for credential-store-backed secrets derived from
// secret_accepts / secret_requires layer.yml entries. They are zero for
// layer-owned secrets (the existing CollectSecretsFromLabels path), preserving
// the current behavior.
//
//   - Service / Key: optional override for the ResolveCredential lookup.
//     Defaults: Service="ov/secret", Key=SecretName. When set, these are
//     passed through resolveSecretValue to the credential store.
//   - RotateOnConfig: if true, ProvisionPodmanSecrets bypasses the
//     podmanSecretExists short-circuit and always rm+creates the podman
//     secret, so rotation via `ov secrets set` + `ov config` takes effect
//     on the next reconcile. Layer-owned secrets (like immich db-password)
//     must keep this false — you cannot re-init a live postgres cluster
//     with a rotated password.
type CollectedSecret struct {
	Name           string // podman secret name: "ov-<image>-<name>"
	Target         string // container mount path
	Env            string // fallback env var name
	SecretName     string // original secret name from layer.yml
	Service        string // credential store service override (empty = use default lookup)
	Key            string // credential store key override (empty = use default lookup)
	RotateOnConfig bool   // if true, bypass podmanSecretExists short-circuit (rotate on every ov config)
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
func ProvisionPodmanSecrets(engine, imageName, instance string, secrets []CollectedSecret, autoGenerate bool) (provisioned []CollectedSecret, fallbackEnv []string, err error) {
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
	// promptedValues caches values entered interactively for a given podman secret name.
	// Two CollectedSecrets sharing the same Name (but different Env vars) only prompt once.
	promptedValues := make(map[string]string)
	interactive := term.IsTerminal(int(os.Stdin.Fd()))

	for _, s := range secrets {
		// Short-circuit: if a podman secret already exists, keep it
		// unconditionally — unless RotateOnConfig is true, in which case we
		// always re-resolve and re-create so credential rotation via
		// `ov secrets set <name> <new>` takes effect on the next ov config.
		//
		// The default (RotateOnConfig=false) is correct for layer-owned
		// secrets like immich's db-password: overwriting would break a live
		// postgres cluster. RotateOnConfig=true is set by
		// CollectLayerSecretAccepts for secret_accepts/secret_requires
		// entries, whose whole point is to reflect the current credential
		// store value on every reconcile. See plan §2.3.
		if !s.RotateOnConfig && podmanSecretExists(engine, s.Name) {
			fmt.Fprintf(os.Stderr, "  %-40s → kept (already provisioned)\n", s.Name)
			provisioned = append(provisioned, s)
			continue
		}

		val, source := resolveSecretValue(s, imageName, instance)
		if val == "" {
			if autoGenerate {
				// Auto-generate: reuse if same podman secret name already generated
				if cached, ok := promptedValues[s.Name]; ok {
					val = cached
					source = "auto-generated"
				} else {
					generated := generateRandomHex(32)
					store := DefaultCredentialStore()
					if storeErr := store.Set("ov/secret", s.Name, generated); storeErr != nil {
						fmt.Fprintf(os.Stderr, "  Warning: could not persist secret '%s': %v\n", s.Name, storeErr)
					}
					promptedValues[s.Name] = generated
					val = generated
					source = "auto-generated"
				}
			} else if interactive {
				if cached, ok := promptedValues[s.Name]; ok {
					val = cached
					source = "user input"
				} else {
					prompt := fmt.Sprintf("Enter value for secret '%s'", s.SecretName)
					if s.Env != "" {
						prompt += fmt.Sprintf(" (%s)", s.Env)
					}
					prompt += ": "
					entered, promptErr := promptPassword(prompt)
					if promptErr != nil {
						fmt.Fprintf(os.Stderr, "  %-40s → prompt failed: %v\n", s.Name, promptErr)
						continue
					}
					if entered == "" {
						fmt.Fprintf(os.Stderr, "  %-40s → skipped (no value entered)\n", s.Name)
						continue
					}
					store := DefaultCredentialStore()
					if storeErr := store.Set("ov/secret", s.Name, entered); storeErr != nil {
						fmt.Fprintf(os.Stderr, "  Warning: could not persist secret '%s': %v\n", s.Name, storeErr)
					}
					promptedValues[s.Name] = entered
					val = entered
					source = "user input"
				}
			} else {
				fmt.Fprintf(os.Stderr, "  %-40s → no value configured\n", s.Name)
				fmt.Fprintf(os.Stderr, "\nWARNING: Secret '%s' has no value configured.\n", s.SecretName)
				fmt.Fprintf(os.Stderr, "The container may fail to start properly.\n\n")
				fmt.Fprintf(os.Stderr, "To set it:\n")
				if s.Env != "" {
					fmt.Fprintf(os.Stderr, "  %s=xxx ov config %s  (env var override)\n", s.Env, imageName)
				}
				fmt.Fprintf(os.Stderr, "  ov secrets set ov/secret %s\n\n", s.Name)
				continue
			}
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
//
// When CollectedSecret.Service and CollectedSecret.Key are both non-empty,
// they take precedence over the default lookup chain: the credential store
// is queried exactly at (Service, Key) with the Env var as the env override.
// This is the path used by secret_accepts / secret_requires entries
// synthesized by CollectLayerSecretAccepts, where the layer author may have
// set `key: ov/api-key/openrouter` to point at a shared credential namespace.
//
// When Service/Key are unset, the default chain (used by layer-owned secrets)
// applies: env var → ov/secret/<podman-name> → ov/secret/<bare-secret-name>.
func resolveSecretValue(s CollectedSecret, imageName, instance string) (value, source string) {
	// Explicit override from CollectLayerSecretAccepts: query exactly once at
	// (Service, Key), allowing the Env var to win via ResolveCredential's
	// env-first chain.
	if s.Service != "" && s.Key != "" {
		val, src := ResolveCredential(s.Env, s.Service, s.Key, "")
		return val, src
	}

	// Default chain for layer-owned secrets (pre-existing behavior).
	// If the secret has an associated env var, check it first.
	if s.Env != "" {
		val, src := ResolveCredential(s.Env, credServiceForSecret(s.Env), credKeyForSecret(imageName, instance), "")
		if val != "" {
			return val, src
		}
	}
	// Try by full podman secret name (e.g. "ov-immich-db-password") — matches `ov secrets set ov/secret ov-immich-db-password`
	if val, src := ResolveCredential("", "ov/secret", s.Name, ""); val != "" {
		return val, src
	}
	// Fallback: try by bare secret name (e.g. "db-password")
	val, src := ResolveCredential("", "ov/secret", s.SecretName, "")
	return val, src
}

// SecretResolution records the result of resolving a single secret_accepts or
// secret_requires entry against the credential store. Returned alongside the
// []CollectedSecret list from CollectLayerSecretAccepts so downstream callers
// (checkMissingSecretRequires in Step 5/6) can distinguish "required but
// missing" from "optional and absent" with actionable remediation.
type SecretResolution struct {
	Name     string // env var name (e.g., "OPENROUTER_API_KEY")
	Source   string // ResolveCredential source classification (env/keyring/kdbx/config/locked/unavailable/default)
	Resolved bool   // true iff a non-empty value was obtained
	Required bool   // true iff the entry came from secret_requires (not secret_accepts)
}

// CollectLayerSecretAccepts synthesizes CollectedSecret entries from an
// image's secret_accepts and secret_requires label metadata, resolving each
// against the credential store and returning:
//
//   - []CollectedSecret: one entry per secret whose value was successfully
//     resolved (non-empty). Entries carry Service/Key overrides from the
//     layer.yml `key:` field (default: ov/secret/<env-var-name>) and
//     RotateOnConfig=true so every ov config reconciles them with the
//     latest credential store value (see plan §2.3).
//   - []SecretResolution: one entry per input spec, reporting the source
//     classification and whether the resolution succeeded. Required entries
//     with Resolved=false are later caught by checkMissingSecretRequires as
//     a hard-fail condition.
//
// This function does NOT touch the podman secret store — that's the job of
// ProvisionPodmanSecrets. It only reads from the credential store. No network
// calls, no filesystem mutations, safe to run speculatively.
func CollectLayerSecretAccepts(imageName, instance string, meta *ImageMetadata) (collected []CollectedSecret, resolutions []SecretResolution) {
	if meta == nil {
		return nil, nil
	}

	resolveOne := func(dep EnvDependency, required bool) {
		// Parse the optional Key override (<service>/<key> form, validated
		// at build time by validateSecretDeps). Default is ov/secret/<name>.
		service := "ov/secret"
		key := dep.Name
		if dep.Key != "" {
			// Key format is already validated (must match ^ov/.../...$).
			// Split on the first '/' after the "ov/" prefix.
			if idx := strings.Index(dep.Key[3:], "/"); idx >= 0 {
				service = dep.Key[:3+idx]
				key = dep.Key[3+idx+1:]
			}
		}

		cs := CollectedSecret{
			Name:           "ov-" + imageName + "-" + envVarNameToPodmanSecretSlug(dep.Name),
			Target:         "", // type=env directive doesn't use Target
			Env:            dep.Name,
			SecretName:     dep.Name,
			Service:        service,
			Key:            key,
			RotateOnConfig: true,
		}

		val, src := resolveSecretValue(cs, imageName, instance)

		res := SecretResolution{
			Name:     dep.Name,
			Source:   src,
			Resolved: val != "",
			Required: required,
		}
		resolutions = append(resolutions, res)

		if val != "" {
			collected = append(collected, cs)
		}
	}

	for _, dep := range meta.SecretRequires {
		resolveOne(dep, true)
	}
	for _, dep := range meta.SecretAccepts {
		resolveOne(dep, false)
	}

	return collected, resolutions
}

// credServiceForSecret maps well-known env vars to credential services.
func credServiceForSecret(envVar string) string {
	switch envVar {
	case "VNC_PASSWORD":
		return CredServiceVNC
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

// podmanSecretExists checks whether a podman secret with the given name already exists.
func podmanSecretExists(engine, name string) bool {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "secret", "inspect", name)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
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
