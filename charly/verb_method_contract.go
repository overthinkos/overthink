package main

// E4: each live-container verb provider self-describes its method contract — the
// Methods() allowlist + the MethodField() accessor for its method-selector field on
// *Op. The host's generic validateCharlyVerb and the method-allowlist bijection gate
// read these through the registry (LiveVerbProvider), replacing the former central
// validateCharlyVerb switch + the liveVerbDispatch registry. The <verb>Methods maps
// for the verbs still defined here (kube/adb/appium — the dep-shedders, extracted
// later) live in checkrun_charly_verbs.go (the verb's dispatch data, used by
// runCharlyVerb). This is the right home for the cross-field required-modifier/artifact
// rules — on the verb that owns them.
//
// cdp/vnc/wl/dbus/mcp/record/spice/libvirt have already been relocated: each owns its
// Methods()/MethodField() contract alongside its provider + <verb>Methods map in its
// dedicated plugin_verb_<verb>.go file.

func (kubeVerb) Methods() map[string]methodSpec { return kubeMethods }
func (kubeVerb) MethodField(c *Op) string       { return c.Kube }

func (adbVerb) Methods() map[string]methodSpec { return adbMethods }
func (adbVerb) MethodField(c *Op) string       { return c.Adb }

// appium has NO in-proc method contract here — it is an EXTERNAL-CHARLY-VERB
// (candy/plugin-appium). Its #AppiumMethod method-name allowlist is enforced by the CUE
// enum on core #Op; its cross-field required-modifier checks run inside the plugin at
// dispatch (the in-proc LiveVerbProvider validateCharlyVerb path no longer applies).
