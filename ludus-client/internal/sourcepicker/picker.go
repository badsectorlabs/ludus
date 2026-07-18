// Package sourcepicker is the interactive picker for selecting blueprints,
// templates, and local roles to install from a registered source.
package sourcepicker

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"ludusapi/dto"
)

// Advanced collects toggleable flags settable from the picker footer.
type Advanced struct {
	Global  bool
	Force   bool
	IsAdmin bool
	// NoDeps skips installing the selected blueprints' galaxy role/collection
	// dependencies. Seeded by --no-deps; toggleable from the footer ([d]).
	NoDeps bool
}

// Run launches the picker. Blocks until commit or abort. committed=false
// means abort (Esc/Ctrl-C/q); the returned selection and Advanced are not
// meaningful in that case. The returned selection is the user's intent set
// (items checked to install) — the caller folds it against the current
// install state to build the wire selection.
func Run(catalog dto.SourceCatalogDTO, adv Advanced) (dto.InstallSelectionDTO, Advanced, bool, error) {
	m := newModel(catalog, adv)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return dto.InstallSelectionDTO{}, adv, false, fmt.Errorf("picker: %w", err)
	}
	fm, ok := final.(model)
	if !ok {
		return dto.InstallSelectionDTO{}, adv, false, fmt.Errorf("picker: unexpected model type %T", final)
	}
	if fm.aborted || !fm.committed {
		return dto.InstallSelectionDTO{}, fm.adv, false, nil
	}
	return fm.currentSelection(), fm.adv, true, nil
}
