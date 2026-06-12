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
		// Keep the input's visible span inside the search-bar box (padding,
		// prompt, cursor cell); the value scrolls horizontally past it.
		m.searchInput.Width = max(8, searchBoxContentWidth(msg.Width)-7)
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
	case tea.KeyDown:
		// Commit the filter like enter, then jump to the first row of the open
		// tab so the next keystroke navigates the results.
		m.searching = false
		m.filter = strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
		m.searchInput.Blur()
		m.clampCursors()
		m.switchTab(m.activeTab) // re-resolve the first non-empty section under the new filter
		m.cursor[m.active] = 0
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
	case "tab", "right", "l":
		m.cycleTab(1)
		return m, nil
	case "shift+tab", "left", "h":
		m.cycleTab(-1)
		return m, nil
	case "1", "2", "3":
		t := tab(msg.String()[0] - '1')
		if t < numTabs && m.tabHasContent(t) {
			m.switchTab(t)
		}
		return m, nil
	case "k":
		m.cursorUp()
		return m, nil
	case "up":
		// At the top of the tab, up ascends into the search bar — the mirror
		// of down committing the filter back into the list. Only the arrow
		// key ascends: a held "k" must not start typing into the input.
		if !m.cursorUp() {
			m.searching = true
			m.searchInput.Focus()
		}
		return m, nil
	case "down", "j":
		m.cursorDown()
		return m, nil
	case " ":
		m.toggleAtCursor()
		return m, nil
	case "a":
		m.toggleAllInTab()
		return m, nil
	case "A":
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

// toggleAllOver: if any visible row across the given sections is unpicked,
// pick everything; otherwise unpick everything. Implied-only rows are skipped
// (they're not user-pickable).
func (m *model) toggleAllOver(secs []section) {
	anyUnpicked := false
outer:
	for _, sec := range secs {
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
	for _, sec := range secs {
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

// toggleAllInTab is `a`: select-all scoped to the visible panel.
func (m *model) toggleAllInTab() {
	m.toggleAllOver(m.activeTab.sections())
}

// toggleAllEverywhere is `A`: select-all across every tab.
func (m *model) toggleAllEverywhere() {
	all := make([]section, 0, numToggleableSections)
	for sec := section(0); sec < numToggleableSections; sec++ {
		all = append(all, sec)
	}
	m.toggleAllOver(all)
}

// tabHasContent reports whether the tab has any rows, ignoring the search
// filter — a tab emptied only by the filter stays visible (showing "no
// matches") so the user knows it exists.
func (m model) tabHasContent(t tab) bool {
	for _, sec := range t.sections() {
		if len(m.allRows(sec)) > 0 {
			return true
		}
	}
	return false
}

// visibleTabs lists the tabs worth showing: those with content in the
// current mode.
func (m model) visibleTabs() []tab {
	var out []tab
	for t := tab(0); t < numTabs; t++ {
		if m.tabHasContent(t) {
			out = append(out, t)
		}
	}
	return out
}

// switchTab activates a tab and drops the cursor on its first section with
// visible rows (falling back to the first section so the highlight always
// lands somewhere sane).
func (m *model) switchTab(t tab) {
	m.activeTab = t
	secs := t.sections()
	m.active = secs[0]
	for _, sec := range secs {
		if len(m.visibleToggleable(sec)) > 0 {
			m.active = sec
			break
		}
	}
}

// cycleTab moves to the next/previous tab with content, wrapping around.
func (m *model) cycleTab(dir int) {
	visible := m.visibleTabs()
	if len(visible) < 2 {
		return
	}
	cur := 0
	for i, t := range visible {
		if t == m.activeTab {
			cur = i
			break
		}
	}
	next := (cur + dir + len(visible)) % len(visible)
	m.switchTab(visible[next])
}

// cursorUp moves the cursor up by one row. At the top of the active
// section, it walks to the previous non-empty section within the active
// tab and lands on that section's last visible row. Returns false when
// already anchored at the top of the tab.
func (m *model) cursorUp() bool {
	if m.cursor[m.active] > 0 {
		m.cursor[m.active]--
		return true
	}
	secs := m.activeTab.sections()
	pos := sectionIndex(secs, m.active)
	for i := pos - 1; i >= 0; i-- {
		visible := m.visibleToggleable(secs[i])
		if len(visible) > 0 {
			m.active = secs[i]
			m.cursor[secs[i]] = len(visible) - 1
			return true
		}
	}
	// Already at the top of the tab's first non-empty section.
	return false
}

// cursorDown moves the cursor down by one row. At the bottom of the
// active section, it walks to the next non-empty section within the active
// tab and lands on that section's first visible row. Anchored at the bottom.
func (m *model) cursorDown() {
	visible := m.visibleToggleable(m.active)
	if m.cursor[m.active] < len(visible)-1 {
		m.cursor[m.active]++
		return
	}
	secs := m.activeTab.sections()
	pos := sectionIndex(secs, m.active)
	for i := pos + 1; i < len(secs); i++ {
		if len(m.visibleToggleable(secs[i])) > 0 {
			m.active = secs[i]
			m.cursor[secs[i]] = 0
			return
		}
	}
	// Already at the bottom of the tab's last non-empty section — no-op.
}

// sectionIndex finds sec's position within secs (0 if absent — callers pass
// the active tab's own section list, so absence means a stale active section
// and starting from the front is the safe recovery).
func sectionIndex(secs []section, sec section) int {
	for i, s := range secs {
		if s == sec {
			return i
		}
	}
	return 0
}

// isImpliedOnly returns true if the row is in templates, local roles, or
// local collections, not explicitly picked, and pulled in by a selected
// blueprint.
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
	case sectionLocalCollections:
		_, ok := implied.LocalCollections[r.id]
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

// matchesFilter returns true if the row matches the current filter (or no
// filter is set). Every whitespace-separated term of the filter must
// fuzzy-match the row's id, label, or description.
func (m model) matchesFilter(r row) bool {
	if m.filter == "" {
		return true
	}
	for _, term := range strings.Fields(m.filter) {
		if !fuzzyMatch(term, r.id) && !fuzzyMatch(term, r.label) && !fuzzyMatch(term, r.description) {
			return false
		}
	}
	return true
}

// fuzzyMatch reports whether term reads as an in-order subsequence of s
// (fzf-style), ignoring case. term must already be lowercase — the filter is
// lowercased on commit.
func fuzzyMatch(term, s string) bool {
	t := []rune(term)
	i := 0
	for _, r := range strings.ToLower(s) {
		if r == t[i] {
			i++
			if i == len(t) {
				return true
			}
		}
	}
	return false
}

// allRows builds the ordered rows for one toggleable section straight from
// the catalog, before any mode/filter trimming. visibleToggleable filters
// these; tabHasContent uses them to keep filter-emptied tabs visible.
func (m model) allRows(sec section) []row {
	var out []row
	switch sec {
	case sectionBlueprints:
		for _, bp := range m.catalog.Blueprints.Items {
			out = append(out, row{
				kind:             rowToggleable,
				section:          sec,
				id:               bp.ID,
				label:            bp.Name,
				description:      bp.Description,
				version:          bp.Version,
				state:            bp.State,
				installedVersion: bp.InstalledVersion,
			})
		}
	case sectionTemplates:
		for _, t := range m.catalog.Templates {
			out = append(out, row{
				kind:             rowToggleable,
				section:          sec,
				id:               t.Name,
				label:            t.Name,
				description:      t.Description,
				version:          t.Version,
				state:            t.State,
				installedVersion: t.InstalledVersion,
				requiredBy:       t.RequiredBy,
			})
		}
	case sectionLocalRoles:
		for _, lr := range m.catalog.LocalRoles {
			out = append(out, row{
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
			})
		}
	case sectionLocalCollections:
		for _, lc := range m.catalog.LocalCollections {
			out = append(out, row{
				kind:             rowToggleable,
				section:          sec,
				id:               lc.Name,
				label:            lc.Name,
				description:      lc.Description,
				version:          lc.Version,
				state:            lc.State,
				installedVersion: lc.InstalledVersion,
				scopes:           lc.Scopes,
				requiredBy:       lc.RequiredBy,
			})
		}
	}
	return out
}

// visibleToggleable returns the filtered, ordered rows for one toggleable
// section. Used for both rendering and cursor math so they stay in sync.
func (m model) visibleToggleable(sec section) []row {
	var out []row
	for _, r := range m.allRows(sec) {
		if m.matchesFilter(r) {
			out = append(out, r)
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
// pulled in by the picked blueprints: the current selection plus blueprints
// already installed (their deps are the steady-state surface, mirroring the
// GUI's required-deps section). Deps required only by blueprints that are
// neither selected nor installed are omitted — they're not going to install,
// so showing them is noise. selectedBy marks the rows the next install acts
// on; deps of merely installed blueprints carry no delta.
func (m model) readOnlyRows(kind readOnlyKind) []row {
	var src []dto.CatalogItemDTO
	switch kind {
	case readOnlyGalaxyRoles:
		src = m.catalog.Blueprints.RequiredRoles
	case readOnlyGalaxyCollections:
		src = m.catalog.Blueprints.RequiredCollections
	case readOnlySubscriptionRoles:
		src = m.catalog.Blueprints.SubscriptionRoles
	}
	// m.picked is map[section.key()]map[itemID]struct{} — the blueprints
	// inner map IS the selected set.
	selected := m.picked[sectionBlueprints.key()]
	picked := map[string]struct{}{}
	for id := range selected {
		picked[id] = struct{}{}
	}
	for _, bp := range m.catalog.Blueprints.Items {
		if bp.State == "installed" || bp.State == "upgrade_available" {
			picked[bp.ID] = struct{}{}
		}
	}

	// Map blueprint ID → display name so the trail says "by GOAD" not
	// "by goad-light". requiredBy on a catalog item carries IDs.
	nameByID := map[string]string{}
	for _, bp := range m.catalog.Blueprints.Items {
		nameByID[bp.ID] = bp.Name
	}

	var out []row
	for _, item := range src {
		// Trim requiredBy to just the picked parents; if none of them are
		// picked, drop the row entirely.
		var byPicked []string
		selectedBy := false
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
			if _, ok := selected[parent]; ok {
				selectedBy = true
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
		// Git collections carry the repo URL in Name; show the resolved FQCN
		// when we have it so the row reads like a collection, not a URL.
		label := item.Name
		if item.Fqcn != "" {
			label = item.Fqcn
		}
		r := row{
			kind:             rowReadOnly,
			id:               item.Name,
			label:            label,
			version:          version,
			state:            item.State,
			installedVersion: item.InstalledVersion,
			scopes:           item.Scopes, // nil for collections → no scope shown
			requiredBy:       byPicked,
			conflictingPins:  conflict,
			selectedBy:       selectedBy,
		}
		if m.matchesFilter(r) {
			out = append(out, r)
		}
	}
	return out
}

// depScopeAction reports what the next install would DO to a read-only dep
// row IN THE SCOPE THIS INSTALL WRITES TO (the [g] toggle: global or user).
// The action is per-scope because that's how installs land — a stale copy in
// a scope this install won't touch isn't something it changes:
//
//	"install"           — a selected blueprint pulls it in and the target
//	                      scope has no copy (additive)
//	"reinstall"         — the target scope has a current copy, but force is on,
//	                      so the install overwrites it in place
//	"upgrade"           — the target scope copy is stale and force is on, so
//	                      the install replaces it with the pinned version
//	"upgrade-available" — same, but force is off: a plain install skips the
//	                      existing copy, so the upgrade is offered, not staged
//	""                  — steady state: no selected parent, already current
//	                      with force off, or deps suppressed (--no-deps)
//
// Force is the crux of the user-visible behavior: with it on, even a
// version- and scope-matching dep is reinstalled, so the chip must reflect
// that rather than reading as a no-op.
func (m model) depScopeAction(r row) string {
	if m.adv.NoDeps || !r.selectedBy {
		return ""
	}
	target := m.targetScope()
	for _, s := range r.scopes {
		if s.Scope != target {
			continue
		}
		if s.State == "upgrade_available" {
			if m.adv.Force {
				return "upgrade"
			}
			return "upgrade-available"
		}
		if m.adv.Force {
			return "reinstall"
		}
		return ""
	}
	// No copy in the target scope. For a scoped row that's an additive
	// install to the target scope. A row with no scope data at all (installed
	// nowhere, or an unscoped kind) falls back to aggregate state.
	if len(r.scopes) == 0 {
		switch r.state {
		case "upgrade_available":
			if m.adv.Force {
				return "upgrade"
			}
			return "upgrade-available"
		case "installed":
			if m.adv.Force {
				return "reinstall"
			}
			return ""
		}
	}
	return "install"
}

// depCountsAsChange reports whether a standard install actually changes this
// dep. install/reinstall/upgrade all mutate the target scope (reinstall and
// upgrade only arise when force is on); upgrade-available does not.
func (m model) depCountsAsChange(r row) bool {
	switch m.depScopeAction(r) {
	case "install", "reinstall", "upgrade":
		return true
	}
	return false
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
