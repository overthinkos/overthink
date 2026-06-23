package main

// E4: each live-container verb provider self-describes its method contract — the
// Methods() allowlist + the MethodField() accessor for its method-selector field on
// *Op. The host's generic validateCharlyVerb and the method-allowlist bijection gate
// read these through the registry (LiveVerbProvider), replacing the former central
// validateCharlyVerb switch + the liveVerbDispatch registry. The <verb>Methods maps
// for the verbs still defined here live in checkrun_charly_verbs.go (the verb's
// dispatch data, used by runCharlyVerb); Phase 1 relocates each into its dedicated
// verb plugin. This is the right home for the cross-field required-modifier/artifact
// rules — on the verb that owns them.
//
// cdp + vnc have already been relocated: each owns its Methods()/MethodField() contract
// alongside its provider + <verb>Methods map in plugin_verb_cdp.go / plugin_verb_vnc.go.

func (wlVerb) Methods() map[string]methodSpec { return wlMethods }
func (wlVerb) MethodField(c *Op) string       { return c.Wl }

func (dbusVerb) Methods() map[string]methodSpec { return dbusMethods }
func (dbusVerb) MethodField(c *Op) string       { return c.Dbus }

func (mcpVerb) Methods() map[string]methodSpec { return mcpMethods }
func (mcpVerb) MethodField(c *Op) string       { return c.Mcp }

func (recordVerb) Methods() map[string]methodSpec { return recordMethods }
func (recordVerb) MethodField(c *Op) string       { return c.Record }

func (spiceVerb) Methods() map[string]methodSpec { return spiceMethods }
func (spiceVerb) MethodField(c *Op) string       { return c.Spice }

func (libvirtVerb) Methods() map[string]methodSpec { return libvirtMethods }
func (libvirtVerb) MethodField(c *Op) string       { return c.Libvirt }

func (kubeVerb) Methods() map[string]methodSpec { return kubeMethods }
func (kubeVerb) MethodField(c *Op) string       { return c.Kube }

func (adbVerb) Methods() map[string]methodSpec { return adbMethods }
func (adbVerb) MethodField(c *Op) string       { return c.Adb }

func (appiumVerb) Methods() map[string]methodSpec { return appiumMethods }
func (appiumVerb) MethodField(c *Op) string       { return c.Appium }
