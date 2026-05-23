package sourcepicker

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"ludusapi/dto"
)

var (
	headerStyle  = lipgloss.NewStyle().Bold(true)
	cursorStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	dimStyle     = lipgloss.NewStyle().Faint(true)
	pickedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	upgradeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	implyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	// Footer chip styles. The "on" color encodes severity: gold for the
	// benign scope flip (global-roles), orange for the actually-risky
	// toggle (force overwrite) so the eye treats them differently. Off
	// stays a neutral grey for both.
	globalOnStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	forceOnStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	offChipStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

// View renders the picker.
func (m model) View() string {
	if m.width == 0 {
		// Pre-resize render: keep it simple, don't try to wrap.
		return "loading picker..."
	}

	var b strings.Builder

	// Header
	sel := m.currentSelection()
	totalPicked := len(sel.Blueprints) + len(sel.Templates) + len(sel.LocalRoles)
	implied := ExpandImplied(m.catalog, sel)
	totalImplied := len(implied.Templates) + len(implied.LocalRoles)
	title := fmt.Sprintf("Pick what to install from %q  (%d selected · %d required)",
		m.catalog.SourceName, totalPicked, totalImplied)
	b.WriteString(headerStyle.Render(truncate(title, m.width)))
	b.WriteString("\n")

	// Keybindings hint
	hint := "/search · [space] toggle · [a] select all · [↑↓] navigate · [tab] jump section · [enter] install · [q] quit"
	b.WriteString(dimStyle.Render(truncate(hint, m.width)))
	b.WriteString("\n\n")

	// Search input (when active)
	if m.searching {
		b.WriteString(m.searchInput.View())
		b.WriteString("\n\n")
	} else if m.filter != "" {
		b.WriteString(dimStyle.Render(fmt.Sprintf("filter: %s", m.filter)))
		b.WriteString("\n\n")
	}

	// Sections — skip entirely when the catalog ships no items of that
	// kind (or the active search filter eliminated them all). Avoids
	// pages of empty "(none)" placeholders that obscure what IS available.
	for sec := section(0); sec < numToggleableSections; sec++ {
		if len(m.visibleToggleable(sec)) == 0 {
			continue
		}
		b.WriteString(m.renderToggleableSection(sec))
		b.WriteString("\n")
	}

	// Read-only sections
	if len(m.catalog.GalaxyRoles) > 0 {
		b.WriteString(m.renderReadOnlySection("Blueprint roles", readOnlyGalaxyRoles))
		b.WriteString("\n")
	}
	if len(m.catalog.GalaxyCollections) > 0 {
		b.WriteString(m.renderReadOnlySection("Blueprint collections", readOnlyGalaxyCollections))
		b.WriteString("\n")
	}
	if len(m.catalog.SubscriptionRoles) > 0 {
		b.WriteString(m.renderReadOnlySection("Subscription roles", readOnlySubscriptionRoles))
		b.WriteString("\n")
	}

	// Footer
	if m.hasUpgradeMismatch() && !m.adv.Force {
		legend := upgradeStyle.Render("△") +
			dimStyle.Render(" version mismatch — toggle [f] force to overwrite, otherwise the affected blueprint may not deploy")
		b.WriteString(truncate(legend, m.width))
		b.WriteString("\n")
	}
	if m.hasConflictingPins() {
		legend := upgradeStyle.Render("△") +
			dimStyle.Render(" conflicting version pins across selected blueprints — ansible-galaxy will install only one; deselect a blueprint to disambiguate")
		b.WriteString(truncate(legend, m.width))
		b.WriteString("\n")
	}
	footer := dimStyle.Render("[g] global roles: ") + chipFor(m.adv.GlobalRoles, m.adv.IsAdmin, globalOnStyle) +
		dimStyle.Render(" · [f] force overwrite: ") + chipFor(m.adv.Force, true, forceOnStyle) +
		dimStyle.Render(" · [a] select all · [enter] install")
	b.WriteString(truncate(footer, m.width))
	b.WriteString("\n")

	return b.String()
}

// chipFor renders an on/off chip for one footer toggle. onStyle lets each
// toggle pick its severity color (gold for benign scope flips, orange for
// risky ones). Non-admin callers see "admin-only" in the global-roles slot
// — they can't flip it.
func chipFor(on, allowed bool, onStyle lipgloss.Style) string {
	if !allowed {
		return dimStyle.Render("admin-only")
	}
	if on {
		return onStyle.Render("on")
	}
	return offChipStyle.Render("off")
}

// hasConflictingPins is true when any visible read-only row reports two or
// more distinct version pins across the currently-selected blueprints.
// Walks readOnlyRows for each kind because the conflict detection lives
// there, not in the catalog itself.
func (m model) hasConflictingPins() bool {
	for _, kind := range []readOnlyKind{
		readOnlyGalaxyRoles,
		readOnlyGalaxyCollections,
		readOnlySubscriptionRoles,
	} {
		for _, r := range m.readOnlyRows(kind) {
			if r.conflictingPins {
				return true
			}
		}
	}
	return false
}

// hasUpgradeMismatch is true when any visible row in the catalog has an
// installed version that differs from the source's required version. Used
// to gate the "△ version mismatch" footer legend so we only nag the user
// when there's actually something to act on.
func (m model) hasUpgradeMismatch() bool {
	check := func(items []dto.CatalogItemDTO) bool {
		for _, it := range items {
			if it.State == "upgrade_available" {
				return true
			}
		}
		return false
	}
	for _, bp := range m.catalog.Blueprints {
		if bp.State == "upgrade_available" {
			return true
		}
	}
	return check(m.catalog.Templates) ||
		check(m.catalog.LocalRoles) ||
		check(m.catalog.GalaxyRoles) ||
		check(m.catalog.GalaxyCollections) ||
		check(m.catalog.SubscriptionRoles)
}

func (m model) renderToggleableSection(sec section) string {
	rows := m.visibleToggleable(sec)
	picked := m.picked[sec.key()]

	pickedCount := 0
	for _, r := range rows {
		if _, ok := picked[r.id]; ok {
			pickedCount++
		}
	}

	heading := fmt.Sprintf("─ %s (%d/%d) ─", sec.title(), pickedCount, len(rows))
	if sec == m.active {
		heading = headerStyle.Render(heading)
	} else {
		heading = dimStyle.Render(heading)
	}

	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")

	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("  (none)\n"))
		return b.String()
	}

	cursorIdx := m.cursor[sec]
	implied := ExpandImplied(m.catalog, m.currentSelection())

	for i, r := range rows {
		var checkbox string
		isPicked := false
		if _, ok := picked[r.id]; ok {
			isPicked = true
		}
		isImplied := false
		if !isPicked && sec != sectionBlueprints {
			switch sec {
			case sectionTemplates:
				_, isImplied = implied.Templates[r.id]
			case sectionLocalRoles:
				_, isImplied = implied.LocalRoles[r.id]
			}
		}
		switch {
		case isPicked:
			checkbox = pickedStyle.Render("[x]")
		case isImplied:
			checkbox = implyStyle.Render("[-]")
		default:
			checkbox = "[ ]"
		}

		cursorMark := "  "
		if sec == m.active && i == cursorIdx {
			cursorMark = cursorStyle.Render("> ")
		}

		state := renderState(r)
		var detail string
		if isImplied && r.state == "not_installed" {
			detail = implyStyle.Render(impliedByString(r.impliedBy))
		} else {
			detail = state
		}

		// Layout per row: <cursor 2><checkbox 3><space><label><space><version 18><space><detail>
		// The label column flexes with terminal width; wraps to a second
		// line when even the flexed column can't fit the name.
		writeRow(&b, cursorMark+checkbox+" ", r.label, r.version, detail, m.width)
	}
	return b.String()
}

func (m model) renderReadOnlySection(title string, kind readOnlyKind) string {
	rows := m.readOnlyRows(kind)
	if len(rows) == 0 {
		return ""
	}
	impliedCount := 0
	for _, r := range rows {
		if len(r.impliedBy) > 0 {
			impliedCount++
		}
	}
	heading := dimStyle.Render(fmt.Sprintf("─ %s (read-only · %d required) ─", title, impliedCount))
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for _, r := range rows {
		detail := renderState(r)
		if len(r.impliedBy) > 0 {
			detail = implyStyle.Render(impliedByString(r.impliedBy))
		}
		version := r.version
		if r.conflictingPins {
			version = upgradeStyle.Render("△ " + version)
		}
		writeRow(&b, "    · ", r.label, version, detail, m.width)
	}
	return b.String()
}

// writeRow renders one picker row, flexing the label column to terminal
// width and wrapping the label to a second line when it would otherwise
// truncate. Continuation indents past the prefix so version/state stay
// aligned with the first-line layout.
//
//	prefix = "> [x] " or "    · " — printable + style escapes, fixed width
//	label  = item name
//	ver    = version string (right-padded to 18 cols when present)
//	detail = installed state / required-by trail
func writeRow(b *strings.Builder, prefix, label, ver, detail string, width int) {
	const versionCol = 18
	const minLabel = 20
	const maxLabel = 60
	prefixWidth := lipgloss.Width(prefix)
	detailWidth := lipgloss.Width(detail)

	// What's left after prefix, version column (with separator), and detail
	// (with separator) goes to the label column.
	available := width - prefixWidth - (versionCol + 1) - (detailWidth + 1)
	labelCol := clamp(available, minLabel, maxLabel)

	if lipgloss.Width(label) <= labelCol {
		line := fmt.Sprintf("%s%-*s %-*s %s", prefix, labelCol, label, versionCol, ver, detail)
		b.WriteString(line)
		b.WriteString("\n")
		return
	}

	// Two-line wrap: full label on line 1, version + detail aligned on line 2.
	b.WriteString(prefix)
	b.WriteString(label)
	b.WriteString("\n")
	indent := strings.Repeat(" ", prefixWidth)
	cont := fmt.Sprintf("%s%-*s %-*s %s", indent, labelCol, "", versionCol, ver, detail)
	b.WriteString(cont)
	b.WriteString("\n")
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func renderState(r row) string {
	switch r.state {
	case "installed":
		return "installed"
	case "upgrade_available":
		// "△ installed v1 → v2" — the triangle is the legend hook; the
		// footer explains what to do about it.
		left := "installed"
		if r.installedVersion != "" {
			left = "installed " + r.installedVersion
		}
		right := "upgrade"
		if r.version != "" {
			right = r.version
		}
		return upgradeStyle.Render("△ " + left + " → " + right)
	case "not_installed", "":
		return dimStyle.Render("not installed")
	}
	return r.state
}

// impliedByString renders the parent name(s) that pulled a read-only row
// in. Caller is responsible for trimming impliedBy to only the currently-
// picked parents (see readOnlyRows). With many parents, show the first
// two and tack on "+N" so long chains don't dominate the line.
func impliedByString(impliedBy []string) string {
	if len(impliedBy) == 0 {
		return ""
	}
	const maxVisible = 2
	if len(impliedBy) <= maxVisible {
		return strings.Join(impliedBy, ", ")
	}
	return fmt.Sprintf("%s +%d",
		strings.Join(impliedBy[:maxVisible], ", "), len(impliedBy)-maxVisible)
}

// truncate clips s to width runes, appending an ellipsis if it had to cut.
// width <= 0 returns s unchanged (avoids divide-by-zero on early renders).
func truncate(s string, width int) string {
	if width <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}
