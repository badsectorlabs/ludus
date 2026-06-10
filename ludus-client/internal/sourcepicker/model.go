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
	sectionLocalCollections
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
	case sectionLocalCollections:
		return "localCollections"
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
		return "Roles"
	case sectionLocalCollections:
		return "Collections"
	}
	return ""
}

// tab is one top-level panel in the tabbed layout. Ansible groups the two
// vendored-content sections, mirroring the GUI's source detail page.
type tab int

const (
	tabBlueprints tab = iota
	tabTemplates
	tabAnsible
	numTabs
)

func (t tab) title() string {
	switch t {
	case tabBlueprints:
		return "Blueprints"
	case tabTemplates:
		return "Templates"
	case tabAnsible:
		return "Ansible"
	}
	return ""
}

// sections lists the toggleable sections rendered inside this tab.
func (t tab) sections() []section {
	switch t {
	case tabBlueprints:
		return []section{sectionBlueprints}
	case tabTemplates:
		return []section{sectionTemplates}
	case tabAnsible:
		return []section{sectionLocalRoles, sectionLocalCollections}
	}
	return nil
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
	adv     Advanced

	// picked[section.key()] = set of IDs/names the user has explicitly checked.
	picked map[string]map[string]struct{}

	// activeTab is the visible panel; active is the section the cursor is in
	// (always one of activeTab's sections).
	activeTab tab
	active    section
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
// nothing checked: a checkbox expresses intent for the current command
// (install this), not the current install state — which the per-row
// indicator shows instead.
func newModel(catalog dto.SourceCatalogDTO, adv Advanced) model {
	ti := textinput.New()
	ti.Placeholder = "filter..."
	ti.Prompt = "/"
	ti.CharLimit = 64

	picked := map[string]map[string]struct{}{
		sectionBlueprints.key():       {},
		sectionTemplates.key():        {},
		sectionLocalRoles.key():       {},
		sectionLocalCollections.key(): {},
	}

	m := model{
		catalog:   catalog,
		adv:       adv,
		picked:    picked,
		activeTab: tabBlueprints,
		active:    sectionBlueprints,
		cursor: map[section]int{
			sectionBlueprints:       0,
			sectionTemplates:        0,
			sectionLocalRoles:       0,
			sectionLocalCollections: 0,
		},
		searchInput: ti,
	}
	// Open on the first tab that actually has content (a source can ship
	// templates/roles without any blueprints).
	for t := tab(0); t < numTabs; t++ {
		if m.tabHasContent(t) {
			m.switchTab(t)
			break
		}
	}
	return m
}
