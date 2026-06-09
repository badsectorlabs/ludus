// Package sourcepicker is the interactive picker for selecting blueprints,
// templates, and local roles to install from a registered source.
package sourcepicker

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"ludusapi/dto"
)

// Mode selects the picker's intent. Install shows every walked item and
// treats a checked box as "install this"; Remove shows only installed items
// and treats a checked box as "drop this". The mode drives the title, the
// commit verb, and which rows are actionable.
type Mode int

const (
	ModeInstall Mode = iota
	ModeRemove
)

// Advanced collects toggleable flags settable from the picker footer.
type Advanced struct {
	Global  bool
	Force   bool
	IsAdmin bool
	// NoDeps skips installing the selected blueprints' galaxy role/collection
	// dependencies. Carried through the picker unchanged (no footer toggle).
	NoDeps bool
}

// Run launches the picker in the given mode. Blocks until commit or abort.
// committed=false means abort (Esc/Ctrl-C/q); the returned selection and
// Advanced are not meaningful in that case. The returned selection is the
// user's intent set (items checked to install, or to drop) — the caller
// folds it against the current install state to build the wire selection.
func Run(catalog dto.SourceCatalogDTO, mode Mode, adv Advanced) (dto.InstallSelectionDTO, Advanced, bool, error) {
	m := newModel(catalog, mode, adv)
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
