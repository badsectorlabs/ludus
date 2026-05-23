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
	version          string
	state            string // not_installed / installed / upgrade_available
	installedVersion string
	impliedBy        []string // for read-only and implied-template rows
	// conflictingPins is true when two or more selected blueprints pinned
	// this role/collection at different versions. The version string in that
	// case is the joined "v1 / v2" rendering; the picker shows a △ so the
	// user knows ansible-galaxy will only install one of them.
	conflictingPins bool
}

// model is the Bubble Tea model backing the picker.
type model struct {
	catalog dto.SourceCatalogDTO
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

// newModel builds an initial model from the catalog and any prior selection.
func newModel(catalog dto.SourceCatalogDTO, initial dto.InstallSelectionDTO, adv Advanced) model {
	ti := textinput.New()
	ti.Placeholder = "filter..."
	ti.Prompt = "/"
	ti.CharLimit = 64

	picked := map[string]map[string]struct{}{
		sectionBlueprints.key(): {},
		sectionTemplates.key():  {},
		sectionLocalRoles.key(): {},
	}
	for _, id := range initial.Blueprints {
		picked[sectionBlueprints.key()][id] = struct{}{}
	}
	for _, n := range initial.Templates {
		picked[sectionTemplates.key()][n] = struct{}{}
	}
	for _, n := range initial.LocalRoles {
		picked[sectionLocalRoles.key()][n] = struct{}{}
	}

	return model{
		catalog: catalog,
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
