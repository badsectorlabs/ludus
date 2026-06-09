// Package sourcepicker is the interactive picker for selecting blueprints,
// templates, and local roles to install from a registered source. The
// picker is a Bubble Tea program; this file contains the pure logic that
// can be unit-tested without driving the TUI.
package sourcepicker

import "ludusapi/dto"

// ImpliedSet is the union of templates and local roles pulled in by the
// currently selected blueprints. The picker shows these as "[-]" rows so
// the user can see what will get installed alongside their explicit picks.
type ImpliedSet struct {
	Templates  map[string]struct{}
	LocalRoles map[string]struct{}
}

// ExpandImplied returns the templates and local roles implied by every
// blueprint in sel. Blueprints not in sel contribute nothing. The result is
// the union across all selected blueprints.
func ExpandImplied(catalog dto.SourceCatalogDTO, sel dto.InstallSelectionDTO) ImpliedSet {
	picked := make(map[string]struct{}, len(sel.Blueprints))
	for _, id := range sel.Blueprints {
		picked[id] = struct{}{}
	}
	out := ImpliedSet{
		Templates:  map[string]struct{}{},
		LocalRoles: map[string]struct{}{},
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
	}
	return out
}
