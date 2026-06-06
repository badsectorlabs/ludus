package sourcepicker

import (
	"github.com/charmbracelet/bubbles/textinput"

	"ludusapi/dto"
)

// section identifies the toggleable groups the cursor can move between.
type section int

const (
	sectionBlueprints section = iota
	sectionTemplates
	sectionLocalRoles
	numToggleableSections
)

func (s section) key() string {
	switch s {
	case sectionBlueprints:
		return "blueprints"
	case sectionTemplates:
		return "templates"
	case sectionLocalRoles:
		return "localRoles"
	}
	return ""
}

func (s section) title() string {
	switch s {
	case sectionBlueprints:
		return "Blueprints"
	case sectionTemplates:
		return "Templates"
	case sectionLocalRoles:
		return "Source roles"
	}
	return ""
}

// rowKind tells the renderer how to draw a row.
type rowKind int

const (
	rowToggleable rowKind = iota
	rowReadOnly           // galaxy/subscription rows
)

// row is one rendered line in the picker. For toggleable rows id is the
// stable lookup key in m.picked[section.key()]. For read-only rows id is
// unused.
type row struct {
	kind             rowKind
	section          section
	id               string // blueprint ID or template/role name
	label            string // primary label
	description      string // optional one-line description (shown for the focused row)
	version          string
	state            string // not_installed / installed / upgrade_available
	installedVersion string
	// scopes lists each installed copy of a role: scope, on-disk version, and
	// per-scope state vs the pin. A role can occupy both global and user at
	// different versions. Empty for non-role kinds and not-installed roles.
	scopes     []dto.ScopeInstallDTO
	requiredBy []string // for read-only and implied-template rows
	// conflictingPins is true when two or more selected blueprints pinned
	// this role/collection at different versions. The version string in that
	// case is the joined "v1 / v2" rendering; the picker shows a △ so the
	// user knows ansible-galaxy will only install one of them.
	conflictingPins bool
}

// model is the Bubble Tea model backing the picker.
type model struct {
	catalog dto.SourceCatalogDTO
	mode    Mode
	adv     Advanced

	// picked[section.key()] = set of IDs/names the user has explicitly checked.
	picked map[string]map[string]struct{}

	// active is the section the cursor is currently in.
	active section
	// cursor is the index within the active section's toggleable rows.
	cursor map[section]int

	// searching: when true, key input goes to searchInput.
	searching   bool
	searchInput textinput.Model
	filter      string // committed filter string (lowercase)

	// width/height from tea.WindowSizeMsg.
	width  int
	height int

	// terminal state
	committed bool
	aborted   bool
}

// newModel builds an initial model from the catalog. The picker opens with
// nothing checked in either mode: a checkbox expresses intent for the current
// command (install this / drop this), not the current install state — which
// the per-row indicator shows instead.
func newModel(catalog dto.SourceCatalogDTO, mode Mode, adv Advanced) model {
	ti := textinput.New()
	ti.Placeholder = "filter..."
	ti.Prompt = "/"
	ti.CharLimit = 64

	picked := map[string]map[string]struct{}{
		sectionBlueprints.key(): {},
		sectionTemplates.key():  {},
		sectionLocalRoles.key(): {},
	}

	return model{
		catalog: catalog,
		mode:    mode,
		adv:     adv,
		picked:  picked,
		active:  sectionBlueprints,
		cursor: map[section]int{
			sectionBlueprints: 0,
			sectionTemplates:  0,
			sectionLocalRoles: 0,
		},
		searchInput: ti,
	}
}

// verb is the action word for the current mode, used in the title and footer.
func (m model) verb() string {
	if m.mode == ModeRemove {
		return "remove"
	}
	return "install"
}

// isInstalledState reports whether a catalog state counts as "currently
// installed" (an upgrade-available item is installed, just at an older
// version). Remove mode only acts on installed items.
func isInstalledState(state string) bool {
	return state == "installed" || state == "upgrade_available"
}
