package main

// labelset.go — unified envelope for the two three-section OCI label
// payloads carried by every overthink image:
//
//   org.overthinkos.tests       → LabelEvalSet (declarative test manifest)
//   org.overthinkos.description → LabelDescriptionSet (BDD self-description)
//
// Pre-2026-04 these types lived in testspec.go and description_spec.go
// respectively. The BDD/test/harness surface-cleanup cutover relocates
// them here and adds a LabelSet aggregate so callers can carry both
// payloads as a single value when convenient. The OCI label wire format
// is unchanged — still TWO labels, parsed separately by ParseLabels in
// labels.go. LabelSet is a Go-side ergonomic aggregate, not a wire
// schema change.
//
// Section semantics (identical for both LabelEvalSet and
// LabelDescriptionSet):
//   - Layer:  one entry per layer in the chain that contributes
//             tests / a description.
//   - Image:  the image's own image-level entries.
//   - Deploy: deploy-scope entries (build-time defaults baked into
//             the image; deploy.yml overlays merge into this section
//             at test/run time, not here).

// LabelSet is the Go-side aggregate of an image's two three-section
// label payloads. Used by call sites that want to pass both around
// together (validators, MCP-style introspection); the existing
// per-label fields (ImageMetadata.Eval, ImageMetadata.Description)
// remain the canonical access points for code that only needs one.
type LabelSet struct {
	Eval         *LabelEvalSet        `json:"eval,omitempty"`
	Descriptions *LabelDescriptionSet `json:"descriptions,omitempty"`
}

// IsEmpty returns true when neither sub-payload carries any entries.
func (s *LabelSet) IsEmpty() bool {
	if s == nil {
		return true
	}
	return s.Eval.IsEmpty() && s.Descriptions.IsEmpty()
}

// LabelEvalSet is the three-section structure embedded in the
// org.overthinkos.tests OCI label: layer-contributed checks, image-level
// checks, and deploy-default checks.
type LabelEvalSet struct {
	Layer  []Check `json:"candy,omitempty"`
	Image  []Check `json:"box,omitempty"`
	Deploy []Check `json:"deploy,omitempty"`
}

// IsEmpty returns true if no section has any checks. Used by label emission
// to omit the label entirely when there are no tests to ship.
func (s *LabelEvalSet) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.Layer) == 0 && len(s.Image) == 0 && len(s.Deploy) == 0
}

// LabelDescriptionSet is the three-section structure embedded in the
// org.overthinkos.description OCI label: layer-contributed descriptions
// (one per layer), image-level description (one), deploy-default
// description (one — usually from deploy.yml overlays).
//
// Mirrors LabelEvalSet's shape so the collection + merge pipeline and
// the reporting format can share a mental model.
type LabelDescriptionSet struct {
	Layer  []LabeledDescription `json:"candy,omitempty"`
	Image  []LabeledDescription `json:"box,omitempty"`
	Deploy []LabeledDescription `json:"deploy,omitempty"`
}

// LabeledDescription is a Description with its collection-time origin
// annotation. Origin follows the `layer:<name>` / `image:<name>` /
// `deploy-default` / `deploy-local` convention also used by
// LabelEvalSet entries' Origin field.
type LabeledDescription struct {
	Origin      string      `json:"origin"`
	Description Description `json:"description"`
}

// IsEmpty returns true if no section has any descriptions. Used by label
// emission to omit the label entirely when there are none.
func (s *LabelDescriptionSet) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.Layer) == 0 && len(s.Image) == 0 && len(s.Deploy) == 0
}
