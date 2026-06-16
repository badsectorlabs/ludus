// Package sourcepicker is the interactive picker for selecting blueprints,
// templates, and local roles to install from a registered source. The
// picker is a Bubble Tea program; this file contains the pure logic that
// can be unit-tested without driving the TUI.
package sourcepicker

import "ludusapi/dto"

// ImpliedSet is the union of templates, local roles, and local collections
// pulled in by the currently selected blueprints. The picker shows these as
// "[-]" rows so the user can see what will get installed alongside their
// explicit picks. The server applies the same implication on install.
type ImpliedSet struct {
	Templates        map[string]struct{}
	LocalRoles       map[string]struct{}
	LocalCollections map[string]struct{}
}

// ExpandImplied returns the templates, local roles, and local collections
// implied by every blueprint in sel. Blueprints not in sel contribute nothing.
// The result is the union across all selected blueprints. RequiredCollections
// names every requirements.yml collection (vendored or galaxy); only names
// that match a local-collection row mark anything in the picker.
func ExpandImplied(catalog dto.SourceCatalogDTO, sel dto.InstallSelectionDTO) ImpliedSet {
	picked := make(map[string]struct{}, len(sel.Blueprints))
	for _, id := range sel.Blueprints {
		picked[id] = struct{}{}
	}
	out := ImpliedSet{
		Templates:        map[string]struct{}{},
		LocalRoles:       map[string]struct{}{},
		LocalCollections: map[string]struct{}{},
	}
	for _, bp := range catalog.Blueprints.Items {
		if _, ok := picked[bp.ID]; !ok {
			continue
		}
		for _, t := range bp.RequiredTemplates {
			out.Templates[t] = struct{}{}
		}
		for _, r := range bp.RequiredLocalRoles {
			out.LocalRoles[r] = struct{}{}
		}
		for _, c := range bp.RequiredCollections {
			out.LocalCollections[c] = struct{}{}
		}
	}
	return out
}
