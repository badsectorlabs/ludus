package sourcepicker

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"ludusapi/dto"
)

// Init satisfies tea.Model. We have no startup commands.
func (m model) Init() tea.Cmd {
	return nil
}

// Update is the Bubble Tea event loop.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.searching {
			return m.updateSearch(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.searching = false
		m.filter = ""
		m.searchInput.SetValue("")
		m.searchInput.Blur()
		m.clampCursors()
		return m, nil
	case tea.KeyEnter:
		m.searching = false
		m.filter = strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
		m.searchInput.Blur()
		m.clampCursors()
		return m, nil
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.filter = strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
	m.clampCursors()
	return m, cmd
}

func (m model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.aborted = true
		return m, tea.Quit
	case "enter":
		m.committed = true
		return m, tea.Quit
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.searchInput.SetValue("")
		return m, nil
	case "tab":
		m.active = (m.active + 1) % numToggleableSections
		return m, nil
	case "shift+tab":
		m.active = (m.active + numToggleableSections - 1) % numToggleableSections
		return m, nil
	case "up", "k":
		m.cursorUp()
		return m, nil
	case "down", "j":
		m.cursorDown()
		return m, nil
	case " ":
		m.toggleAtCursor()
		return m, nil
	case "a":
		m.toggleAllEverywhere()
		return m, nil
	case "g":
		if m.adv.IsAdmin {
			m.adv.Global = !m.adv.Global
		}
		return m, nil
	case "f":
		m.adv.Force = !m.adv.Force
		return m, nil
	}
	return m, nil
}

// toggleAtCursor flips the picked state of the row under the active cursor.
// Implied rows can't be toggled directly — you check the blueprint that pulls
// them in, not the implied item itself.
func (m *model) toggleAtCursor() {
	rows := m.visibleToggleable(m.active)
	if len(rows) == 0 {
		return
	}
	idx := m.cursor[m.active]
	if idx < 0 || idx >= len(rows) {
		return
	}
	r := rows[idx]
	if m.isImpliedOnly(r) {
		return
	}
	set := m.picked[m.active.key()]
	if _, ok := set[r.id]; ok {
		delete(set, r.id)
	} else {
		set[r.id] = struct{}{}
	}
}

// toggleAllEverywhere: if any visible row across any toggleable section is
// unpicked, pick everything; otherwise unpick everything. Implied-only rows
// are skipped (they're not user-pickable). Spans all three toggleable
// sections so `a` is a true "select all" — section-local toggling is no
// longer a separate keybind.
func (m *model) toggleAllEverywhere() {
	anyUnpicked := false
outer:
	for sec := section(0); sec < numToggleableSections; sec++ {
		set := m.picked[sec.key()]
		for _, r := range m.visibleToggleable(sec) {
			if m.isImpliedOnly(r) {
				continue
			}
			if _, ok := set[r.id]; !ok {
				anyUnpicked = true
				break outer
			}
		}
	}
	for sec := section(0); sec < numToggleableSections; sec++ {
		set := m.picked[sec.key()]
		for _, r := range m.visibleToggleable(sec) {
			if m.isImpliedOnly(r) {
				continue
			}
			if anyUnpicked {
				set[r.id] = struct{}{}
			} else {
				delete(set, r.id)
			}
		}
	}
}

// cursorUp moves the cursor up by one row. At the top of the active
// section, it walks to the previous non-empty toggleable section and
// lands on that section's last visible row. Anchored at the very top.
func (m *model) cursorUp() {
	if m.cursor[m.active] > 0 {
		m.cursor[m.active]--
		return
	}
	for sec := m.active - 1; sec >= 0; sec-- {
		visible := m.visibleToggleable(sec)
		if len(visible) > 0 {
			m.active = sec
			m.cursor[sec] = len(visible) - 1
			return
		}
	}
	// Already at the top of the first non-empty section — no-op.
}

// cursorDown moves the cursor down by one row. At the bottom of the
// active section, it walks to the next non-empty toggleable section and
// lands on that section's first visible row. Anchored at the very bottom.
func (m *model) cursorDown() {
	visible := m.visibleToggleable(m.active)
	if m.cursor[m.active] < len(visible)-1 {
		m.cursor[m.active]++
		return
	}
	for sec := m.active + 1; sec < numToggleableSections; sec++ {
		if len(m.visibleToggleable(sec)) > 0 {
			m.active = sec
			m.cursor[sec] = 0
			return
		}
	}
	// Already at the bottom of the last non-empty section — no-op.
}

// isImpliedOnly returns true if the row is in templates or local roles, not
// explicitly picked, and pulled in by a selected blueprint.
func (m model) isImpliedOnly(r row) bool {
	if r.kind != rowToggleable {
		return false
	}
	if r.section == sectionBlueprints {
		return false
	}
	if _, picked := m.picked[r.section.key()][r.id]; picked {
		return false
	}
	implied := ExpandImplied(m.catalog, m.currentSelection())
	switch r.section {
	case sectionTemplates:
		_, ok := implied.Templates[r.id]
		return ok
	case sectionLocalRoles:
		_, ok := implied.LocalRoles[r.id]
		return ok
	}
	return false
}

// currentSelection flattens the picked maps into a deterministic
// InstallSelectionDTO. Slices are sorted so tests can assert easily.
func (m model) currentSelection() dto.InstallSelectionDTO {
	out := dto.InstallSelectionDTO{}
	out.Blueprints = setToSortedSlice(m.picked[sectionBlueprints.key()])
	out.Templates = setToSortedSlice(m.picked[sectionTemplates.key()])
	out.LocalRoles = setToSortedSlice(m.picked[sectionLocalRoles.key()])
	out.LocalCollections = setToSortedSlice(m.picked[sectionLocalCollections.key()])
	return out
}

func setToSortedSlice(s map[string]struct{}) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// visibleInMode reports whether a row should appear given the picker mode.
// Remove mode hides anything not currently installed — you can't drop what
// isn't there. Install mode shows the whole walked catalog.
func (m model) visibleInMode(r row) bool {
	if m.mode == ModeRemove {
		return isInstalledState(r.state)
	}
	return true
}

// matchesFilter returns true if the row matches the current filter (or no
// filter is set).
func (m model) matchesFilter(r row) bool {
	if m.filter == "" {
		return true
	}
	q := m.filter
	if strings.Contains(strings.ToLower(r.id), q) {
		return true
	}
	if strings.Contains(strings.ToLower(r.label), q) {
		return true
	}
	return false
}

// visibleToggleable returns the filtered, ordered rows for one toggleable
// section. Used for both rendering and cursor math so they stay in sync.
func (m model) visibleToggleable(sec section) []row {
	var out []row
	switch sec {
	case sectionBlueprints:
		for _, bp := range m.catalog.Blueprints.Items {
			r := row{
				kind:             rowToggleable,
				section:          sec,
				id:               bp.ID,
				label:            bp.Name,
				description:      bp.Description,
				version:          bp.Version,
				state:            bp.State,
				installedVersion: bp.InstalledVersion,
			}
			if m.matchesFilter(r) && m.visibleInMode(r) {
				out = append(out, r)
			}
		}
	case sectionTemplates:
		for _, t := range m.catalog.Templates {
			r := row{
				kind:             rowToggleable,
				section:          sec,
				id:               t.Name,
				label:            t.Name,
				description:      t.Description,
				version:          t.Version,
				state:            t.State,
				installedVersion: t.InstalledVersion,
				requiredBy:       t.RequiredBy,
			}
			if m.matchesFilter(r) && m.visibleInMode(r) {
				out = append(out, r)
			}
		}
	case sectionLocalRoles:
		for _, lr := range m.catalog.LocalRoles {
			r := row{
				kind:             rowToggleable,
				section:          sec,
				id:               lr.Name,
				label:            lr.Name,
				description:      lr.Description,
				version:          lr.Version,
				state:            lr.State,
				installedVersion: lr.InstalledVersion,
				scopes:           lr.Scopes,
				requiredBy:       lr.RequiredBy,
			}
			if m.matchesFilter(r) && m.visibleInMode(r) {
				out = append(out, r)
			}
		}
	case sectionLocalCollections:
		for _, lc := range m.catalog.LocalCollections {
			r := row{
				kind:             rowToggleable,
				section:          sec,
				id:               lc.Name,
				label:            lc.Name,
				description:      lc.Description,
				version:          lc.Version,
				state:            lc.State,
				installedVersion: lc.InstalledVersion,
				requiredBy:       lc.RequiredBy,
			}
			if m.matchesFilter(r) && m.visibleInMode(r) {
				out = append(out, r)
			}
		}
	}
	return out
}

// readOnlyKind selects which read-only catalog section to draw.
type readOnlyKind int

const (
	readOnlyGalaxyRoles readOnlyKind = iota
	readOnlyGalaxyCollections
	readOnlySubscriptionRoles
)

// readOnlyRows returns the rows for a read-only section, filtered to items
// pulled in by the currently selected blueprints. Roles/collections required
// only by unselected blueprints are omitted — they're not going to install,
// so showing them is noise.
func (m model) readOnlyRows(kind readOnlyKind) []row {
	var src []dto.CatalogItemDTO
	switch kind {
	case readOnlyGalaxyRoles:
		src = m.catalog.Blueprints.RequiredRoles
	case readOnlyGalaxyCollections:
		src = m.catalog.Blueprints.RequiredCollections
	case readOnlySubscriptionRoles:
		src = m.catalog.SubscriptionRoles
	}
	// m.picked is map[section.key()]map[itemID]struct{} — the blueprints
	// inner map IS the picked set.
	picked := m.picked[sectionBlueprints.key()]

	// Map blueprint ID → display name so the trail says "by GOAD" not
	// "by goad-light". requiredBy on a catalog item carries IDs.
	nameByID := map[string]string{}
	for _, bp := range m.catalog.Blueprints.Items {
		nameByID[bp.ID] = bp.Name
	}

	var out []row
	for _, item := range src {
		// Trim requiredBy to just the parents the user actually picked; if
		// none of them are picked, drop the row entirely.
		var byPicked []string
		// Collect the distinct version pins from the picked blueprints. When
		// the picked set is empty for an item, the row is dropped. When two
		// or more picked blueprints pinned the same item at DIFFERENT
		// versions, the row is marked as conflicting and the version cell
		// renders all pins side-by-side. A blueprint that doesn't pin
		// contributes no version — we don't fall back to other blueprints'
		// pins because the install behavior for an unpinned blueprint is
		// "use whatever ansible-galaxy resolves," not "use that other pin."
		seenPins := map[string]bool{}
		var distinctPins []string
		for _, parent := range item.RequiredBy {
			if _, ok := picked[parent]; !ok {
				continue
			}
			name := nameByID[parent]
			if name == "" {
				name = parent
			}
			byPicked = append(byPicked, name)
			if v, ok := item.VersionByBlueprint[parent]; ok && v != "" {
				if !seenPins[v] {
					seenPins[v] = true
					distinctPins = append(distinctPins, v)
				}
			}
		}
		if len(byPicked) == 0 {
			continue
		}
		version := ""
		conflict := false
		switch len(distinctPins) {
		case 0:
			// No picked blueprint pinned a version — render blank; the row
			// label alone tells the user the role/collection will install
			// at whatever ansible-galaxy resolves.
		case 1:
			version = distinctPins[0]
		default:
			// Two or more picked blueprints disagree. Surface all pins; the
			// footer legend explains the △ and that only one will install.
			version = strings.Join(distinctPins, " / ")
			conflict = true
		}
		r := row{
			kind:            rowReadOnly,
			id:              item.Name,
			label:           item.Name,
			version:         version,
			state:           item.State,
			scopes:          item.Scopes, // nil for collections → no scope shown
			requiredBy:      byPicked,
			conflictingPins: conflict,
		}
		if m.matchesFilter(r) {
			out = append(out, r)
		}
	}
	return out
}

// clampCursors keeps every section cursor within its visible-row range so
// filter changes don't leave us pointing past the end of a list.
func (m *model) clampCursors() {
	for sec := section(0); sec < numToggleableSections; sec++ {
		n := len(m.visibleToggleable(sec))
		if n == 0 {
			m.cursor[sec] = 0
			continue
		}
		if m.cursor[sec] >= n {
			m.cursor[sec] = n - 1
		}
		if m.cursor[sec] < 0 {
			m.cursor[sec] = 0
		}
	}
}
