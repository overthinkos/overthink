package migrate

// credential_inject.go — the credential-store seam the charly-cutover4 migrator's
// keyring re-key needs. charly core injects its real store (DefaultCredentialStore)
// via SetCredentialStoreProvider at startup; the candy never imports core. A nil
// provider ⇒ the keyring re-key is skipped (it is host-gated on ctx.HostDeployPath
// and not exercised by unit tests).

// CredentialStore is the minimal OS-keyring contract cutover4 uses to move
// credentials from the ov/<svc> namespace to charly/<svc>.
type CredentialStore interface {
	Get(service, key string) (string, error)
	Set(service, key, value string) error
	Delete(service, key string) error
	List(service string) ([]string, error)
}

// credentialStoreProvider is the host-injected store accessor (nil until core
// calls SetCredentialStoreProvider).
var credentialStoreProvider func() CredentialStore

// SetCredentialStoreProvider injects the host credential store so the compiled-in
// cutover4 keyring re-key reaches the real OS keyring. Called from charly core.
func SetCredentialStoreProvider(f func() CredentialStore) { credentialStoreProvider = f }

// actVerbsSet is the act-verb set (VerbCatalog DoAct keys) the plan-unify migrator
// consults to classify a bare-verb step with no explicit do:. VerbCatalog is
// package-main, so charly core INJECTS the set once at startup via SetActVerbs
// (it is a compile-time constant — set once, never per-run). nil ⇒ a bare-verb
// step classifies as check (the conservative default).
var actVerbsSet map[string]bool

// SetActVerbs injects the VerbCatalog act-verb set. Called from charly core at
// startup so the compiled-in plan-unify migrator classifies install verbs as run:.
func SetActVerbs(verbs []string) {
	m := make(map[string]bool, len(verbs))
	for _, v := range verbs {
		m[v] = true
	}
	actVerbsSet = m
}
