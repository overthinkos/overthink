module github.com/overthinkos/overthink/candy/plugin-cdp

go 1.26.0

require github.com/overthinkos/overthink/charly v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// Local build: charly's git-repo plugin loader builds this on the host against the
// in-tree charly (kit + spec). A published external plugin would require a tagged
// charly version instead.
replace github.com/overthinkos/overthink/charly => ../../charly
