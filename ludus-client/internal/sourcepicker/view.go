package sourcepicker

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"ludusapi/dto"
)

// Palette. Colors are adaptive so the accent stays readable on both light
// and dark terminals — the old fixed bright-gold (256-color 220) washed out
// to nothing on a light background (e.g. Ghostty light). Each role picks a
// dark, saturated value for light backgrounds and the original bright value
// for dark ones.
var (
	accentColor    = lipgloss.AdaptiveColor{Light: "136", Dark: "220"} // selection/active highlight (gold)
	warnColor      = lipgloss.AdaptiveColor{Light: "166", Dark: "214"} // upgrade / risky toggle (orange)
	installedColor = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}   // installed indicator (green)
	offColor       = lipgloss.AdaptiveColor{Light: "240", Dark: "244"} // muted "off" chip grey
)

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	// titleStyle is the source-name banner. Terminals have no font sizes, so
	// "larger" is approximated with the boldest weight plus the accent color.
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	cursorStyle  = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	dimStyle     = lipgloss.NewStyle().Faint(true)
	pickedStyle  = lipgloss.NewStyle().Foreground(accentColor)
	upgradeStyle = lipgloss.NewStyle().Foreground(warnColor)
	implyStyle   = lipgloss.NewStyle().Foreground(accentColor)
	// willInstallStyle is the chip on dependencies the current selection
	// would pull in; willUpgradeStyle marks the ones a force install would
	// bump in place.
	willInstallStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	willUpgradeStyle = lipgloss.NewStyle().Bold(true).Foreground(warnColor)
	installedStyle   = lipgloss.NewStyle().Foreground(installedColor)
	// Footer chip styles. The "on" color encodes severity: gold for the
	// benign scope flip (global), orange for the actually-risky toggles
	// (force overwrite, skip deps) so the eye treats them differently. Off
	// stays a neutral grey for all.
	globalOnStyle   = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	forceOnStyle    = lipgloss.NewStyle().Bold(true).Foreground(warnColor)
	skipDepsOnStyle = lipgloss.NewStyle().Bold(true).Foreground(warnColor)
	offChipStyle    = lipgloss.NewStyle().Foreground(offColor)
	// Control-bar styles (the top hint + bottom footer). Keycaps are bold at
	// full intensity so they pop; the surrounding labels stay a readable muted
	// grey. Both sit above the faint legend, so the control bar reads as a
	// distinct band rather than blending into the contextual △ warnings.
	keyStyle     = lipgloss.NewStyle().Bold(true)
	controlStyle = lipgloss.NewStyle().Foreground(offColor)
	// Search-bar chrome: a bordered box so the filter reads as a search bar.
	// Accent border while typing; muted grey once the filter is committed.
	searchBoxStyle     = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(accentColor).Padding(0, 1)
	searchBoxIdleStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(offColor).Padding(0, 1)
)

// keyTokenRe matches the actionable tokens in a control hint — bracketed
// keys like "[enter]" and the bare "/search" — so styleControls can bold them
// while leaving the descriptive words muted.
var keyTokenRe = regexp.MustCompile(`\[[^\]]*\]|/search`)

// styleControls renders a plain hint string with its keycaps bold and the rest
// in the muted control color. Callers truncate the PLAIN string first, then
// pass it here — styling adds escape codes that would throw off a later width
// count.
func styleControls(s string) string {
	var b strings.Builder
	last := 0
	for _, loc := range keyTokenRe.FindAllStringIndex(s, -1) {
		if loc[0] > last {
			b.WriteString(controlStyle.Render(s[last:loc[0]]))
		}
		b.WriteString(keyStyle.Render(s[loc[0]:loc[1]]))
		last = loc[1]
	}
	if last < len(s) {
		b.WriteString(controlStyle.Render(s[last:]))
	}
	return b.String()
}

// View renders the picker: a source header, the tab strip, the active
// tab's panel windowed to the leftover height, and the footer controls.
func (m model) View() string {
	if m.width == 0 {
		// Pre-resize render: keep it simple, don't try to wrap.
		return "loading picker..."
	}

	// Top block — source header, optional search line, tab strip.
	var top strings.Builder

	top.WriteString(titleStyle.Render(truncate(m.catalog.SourceName, m.width)))
	top.WriteString("\n")
	if desc := strings.TrimSpace(m.catalog.Description); desc != "" {
		for _, dl := range wrapClamp(desc, (m.width*4)/5, 3) {
			top.WriteString(dl)
			top.WriteString("\n")
		}
	}
	if meta := m.sourceMetaLine(); meta != "" {
		top.WriteString(dimStyle.Render(truncate(meta, m.width)))
		top.WriteString("\n")
	}
	top.WriteString("\n")

	if m.searching {
		top.WriteString(searchBoxStyle.Width(searchBoxContentWidth(m.width)).Render(m.searchInput.View()))
		top.WriteString("\n\n")
	} else if m.filter != "" {
		committed := "🔍 " + dimStyle.Render(truncate(m.filter, max(0, searchBoxContentWidth(m.width)-6)))
		top.WriteString(searchBoxIdleStyle.Width(searchBoxContentWidth(m.width)).Render(committed))
		top.WriteString("\n\n")
	}

	top.WriteString(truncate(m.renderTabStrip(), m.width))
	top.WriteString("\n\n")

	// Bottom block — install-time △ legends, then the control bar.
	var bottom strings.Builder
	if m.hasUpgradeMismatch() {
		outcome := dimStyle.Render(" · [f] force to overwrite it")
		if m.adv.Force {
			outcome = dimStyle.Render(" · [f] force is on — install will overwrite it")
		}
		legend := upgradeStyle.Render("△") +
			dimStyle.Render(fmt.Sprintf(" a selected blueprint needs a newer version than your %s install", m.targetScope())) +
			outcome
		bottom.WriteString(truncate(legend, m.width))
		bottom.WriteString("\n")
	}
	// Pin ambiguity only matters when deps actually install.
	if !m.adv.NoDeps && m.hasConflictingPins() {
		legend := upgradeStyle.Render("△") +
			dimStyle.Render(" conflicting version pins across the responsible blueprints — ansible-galaxy will install only one; drop a blueprint to disambiguate")
		bottom.WriteString(truncate(legend, m.width))
		bottom.WriteString("\n")
	}
	// Control bar: the key hint, then the toggle chips. Keycaps bold at full
	// intensity, labels muted. Hint is truncated as a plain string first —
	// styleControls adds escape codes a later width count couldn't see through.
	hint := "/search · [space] toggle · [a] all in tab · [tab] switch · [enter] install · [q] quit"
	bottom.WriteString("\n")
	bottom.WriteString(styleControls(truncate(hint, m.width)))
	bottom.WriteString("\n")
	// Chips built pre-styled (no truncate — escape codes would inflate a
	// width count and clip it); the visible width is bounded and small.
	sep := controlStyle.Render(" · ")
	chips := keyStyle.Render("[g]") + controlStyle.Render(" global: ") +
		chipFor(m.adv.Global, m.adv.IsAdmin, globalOnStyle) +
		sep + keyStyle.Render("[f]") + controlStyle.Render(" force overwrite: ") +
		chipFor(m.adv.Force, true, forceOnStyle) +
		sep + keyStyle.Render("[d]") + controlStyle.Render(" skip deps: ") +
		chipFor(m.adv.NoDeps, true, skipDepsOnStyle)
	bottom.WriteString(chips)
	bottom.WriteString("\n")

	// The active panel gets whatever height the fixed blocks leave over.
	topStr, bottomStr := top.String(), bottom.String()
	panelHeight := m.height - strings.Count(topStr, "\n") - strings.Count(bottomStr, "\n")
	lines, cursorLine := m.panelLines()
	windowed := windowLines(lines, cursorLine, panelHeight)

	return topStr + strings.Join(windowed, "\n") + "\n" + bottomStr
}

// sourceMetaLine is the header's third line: "Synced 2h ago" /
// "Uploaded just now". Empty when the catalog doesn't carry a sync time
// (older servers).
func (m model) sourceMetaLine() string {
	when := relTime(m.catalog.LastSyncedAt)
	if when == "" {
		return ""
	}
	verb := "Synced"
	if m.catalog.SourceType == "upload" {
		verb = "Uploaded"
	}
	return verb + " " + when
}

// renderTabStrip draws the tab bar: each content-bearing tab with its
// picked/total counts, the active one highlighted. Counts follow the search
// filter so a global "/" search shows which tabs hold matches.
func (m model) renderTabStrip() string {
	var parts []string
	for _, t := range m.visibleTabs() {
		total, picked := 0, 0
		for _, sec := range t.sections() {
			rows := m.visibleToggleable(sec)
			total += len(rows)
			set := m.picked[sec.key()]
			for _, r := range rows {
				if _, ok := set[r.id]; ok {
					picked++
				}
			}
		}
		label := fmt.Sprintf("%s %d/%d", t.title(), picked, total)
		if t == m.activeTab {
			parts = append(parts, cursorStyle.Render(label))
		} else {
			parts = append(parts, dimStyle.Render(label))
		}
	}
	return "  " + strings.Join(parts, dimStyle.Render(" │ "))
}

// windowLines clips the panel to height lines around the cursor (kept
// roughly centered) and overlays dim "more" markers on the clipped edges.
// The cursor can never land on a marker: a top marker implies offset > 0 so
// the centered cursor sits below line 0, and a bottom marker implies the
// window ends before the list does.
func windowLines(lines []string, cursorLine, height int) []string {
	if height < 3 {
		height = 3
	}
	if len(lines) <= height {
		return lines
	}
	off := cursorLine - height/2
	if off < 0 {
		off = 0
	}
	if max := len(lines) - height; off > max {
		off = max
	}
	out := make([]string, height)
	copy(out, lines[off:off+height])
	if off > 0 {
		out[0] = dimStyle.Render(fmt.Sprintf("  ↑ %d more", off))
	}
	if rest := len(lines) - (off + height); rest > 0 {
		out[height-1] = dimStyle.Render(fmt.Sprintf("  ↓ %d more", rest))
	}
	return out
}

// relTime renders a server timestamp as a coarse relative age — the picker
// header doesn't need precision. Accepts RFC3339 and PocketBase's DateTime
// string form; anything else (or empty) yields "".
func relTime(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05.000Z", s)
	}
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

// chipFor renders an on/off chip for one footer toggle. onStyle lets each
// toggle pick its severity color (gold for benign scope flips, orange for
// risky ones). Non-admin callers see "admin-only" in the global slot
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

// hasUpgradeMismatch is true when a dependency pulled in by a CURRENTLY
// SELECTED blueprint has a stale copy in the scope this install writes to —
// exactly the rows whose delta is "upgrade". Deps of merely installed
// blueprints never fire this: the next install doesn't touch them.
func (m model) hasUpgradeMismatch() bool {
	for _, kind := range []readOnlyKind{
		readOnlyGalaxyRoles,
		readOnlyGalaxyCollections,
		readOnlySubscriptionRoles,
	} {
		for _, r := range m.readOnlyRows(kind) {
			switch m.depScopeAction(r) {
			case "upgrade", "upgrade-available":
				return true
			}
		}
	}
	return false
}

// targetScope is the scope an install/force writes to, per the [g] global
// toggle: "global" (the system-wide roles path) when on, "user" (the owner's
// per-user path) when off.
func (m model) targetScope() string {
	if m.adv.Global {
		return "global"
	}
	return "user"
}

// panelLines builds the active tab's content as one line per terminal row,
// plus the line index the cursor sits on (for windowing). Single-section
// tabs render rows flush — the tab strip already names them; the Ansible
// tab labels its two sub-lists. The Blueprints tab appends the read-only
// dependency sections underneath, exactly as the stacked layout did.
func (m model) panelLines() ([]string, int) {
	var lines []string
	cursorLine := 0
	secs := m.activeTab.sections()
	multi := len(secs) > 1

	anyRows := false
	for _, sec := range secs {
		rows := m.visibleToggleable(sec)
		if len(rows) == 0 {
			continue
		}
		anyRows = true
		if multi {
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
			lines = append(lines, heading)
		}
		secLines, secCursor := m.rowLines(sec, rows)
		if sec == m.active && secCursor >= 0 {
			cursorLine = len(lines) + secCursor
		}
		lines = append(lines, secLines...)
		if multi {
			lines = append(lines, "")
		}
	}
	if !anyRows {
		lines = append(lines, dimStyle.Render("  (no matches in this tab)"))
	}

	// The blueprint dependency section is read-only: deps install with their
	// blueprint and are removed via the ansible delete commands, not here.
	if m.activeTab == tabBlueprints {
		if s := m.renderDepsSection(); s != "" {
			lines = append(lines, "")
			lines = append(lines, strings.Split(strings.TrimRight(s, "\n"), "\n")...)
		}
	}
	return lines, cursorLine
}

// rowLines renders one section's toggleable rows, returning the lines and
// the index (within them) of the cursor row, or -1 when the cursor isn't in
// this section.
func (m model) rowLines(sec section, rows []row) ([]string, int) {
	picked := m.picked[sec.key()]
	cursorIdx := m.cursor[sec]
	implied := ExpandImplied(m.catalog, m.currentSelection())

	var lines []string
	cursorLine := -1
	for i, r := range rows {
		var checkbox string
		isPicked := false
		if _, ok := picked[r.id]; ok {
			isPicked = true
		}
		// Implied rows ([-]): a template/role/collection pulled in by a
		// checked blueprint.
		isImplied := false
		if !isPicked && sec != sectionBlueprints {
			switch sec {
			case sectionTemplates:
				_, isImplied = implied.Templates[r.id]
			case sectionLocalRoles:
				_, isImplied = implied.LocalRoles[r.id]
			case sectionLocalCollections:
				_, isImplied = implied.LocalCollections[r.id]
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
			cursorLine = len(lines)
		}

		detail := renderState(r)
		if isImplied && len(r.requiredBy) > 0 {
			detail += "  " + requiredByTrail(r.requiredBy)
		}

		// Layout per row: <cursor 2><checkbox 3><space><label><space><version 18><space><detail>
		// The label column flexes with terminal width; wraps to a second
		// line when even the flexed column can't fit the name.
		lines = append(lines, rowStrings(cursorMark+checkbox+" ", r.label, formatVersion(r.version), detail, m.width)...)

		// Show the focused row's description as a dim subtitle indented under
		// the label. Cursor-only keeps the list compact for long catalogs.
		if sec == m.active && i == cursorIdx && strings.TrimSpace(r.description) != "" {
			for _, dl := range wrapClamp(r.description, max(10, ((m.width*4)/5-6)*4/5), 5) {
				lines = append(lines, dimStyle.Render(truncate("      "+dl, m.width)))
			}
		}
	}
	return lines, cursorLine
}

// renderDepsSection draws the read-only dependency closure under the
// blueprints list, modeled on the GUI's "Ansible dependencies" section: one
// header carrying the row total and the count of changes the next install
// would make, then Roles (galaxy + subscription) and Collections subheadings.
// Each row gets a delta chip — WILL INSTALL / WILL UPGRADE / UPGRADE
// AVAILABLE — only when a SELECTED blueprint pulls it in; deps of merely
// installed blueprints render as steady state.
func (m model) renderDepsSection() string {
	roles := append(m.readOnlyRows(readOnlyGalaxyRoles), m.readOnlyRows(readOnlySubscriptionRoles)...)
	collections := m.readOnlyRows(readOnlyGalaxyCollections)
	total := len(roles) + len(collections)
	if total == 0 {
		return ""
	}

	pending := 0
	for _, r := range roles {
		if m.depCountsAsChange(r) {
			pending++
		}
	}
	for _, r := range collections {
		if m.depCountsAsChange(r) {
			pending++
		}
	}

	heading := fmt.Sprintf("ANSIBLE DEPENDENCIES %d", total)
	if m.adv.NoDeps {
		heading += dimStyle.Render(" · ") + skipDepsOnStyle.Render("skipped") +
			dimStyle.Render(" — [d] skip deps is on")
	} else if pending > 0 {
		change := "changes"
		if pending == 1 {
			change = "change"
		}
		heading += dimStyle.Render(" · ") + willInstallStyle.Render(fmt.Sprintf("%d %s on install", pending, change))
	}

	var b strings.Builder
	b.WriteString(truncate(heading, m.width))
	b.WriteString("\n")
	if len(roles) > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  ROLES %d", len(roles))))
		b.WriteString("\n")
		m.writeDepRows(&b, roles)
	}
	if len(collections) > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  COLLECTIONS %d", len(collections))))
		b.WriteString("\n")
		m.writeDepRows(&b, collections)
	}
	return b.String()
}

// writeDepRows renders one subheading's dep rows. The top line is the name
// with its pin and the "← <parent> +N" trail; the install state — and what
// the next install will do to it — lives in the per-scope sublines below.
// Each installed scope shows its copy; the scope this install targets has its
// subline rewritten in place when the install would reinstall/upgrade it
// (force), and a "WILL INSTALL" subline is added when the target scope has no
// copy yet (additive). A row installed nowhere, with nothing pending, falls
// back to a single steady-state line.
func (m model) writeDepRows(b *strings.Builder, rows []row) {
	nameFor := func(r row) string {
		n := r.label
		if v := formatVersion(r.version); v != "" {
			if r.conflictingPins {
				v = "△ " + v
			}
			n += " " + v
		}
		return n
	}
	nameCol := 0
	for _, r := range rows {
		if w := lipgloss.Width(nameFor(r)); w > nameCol {
			nameCol = w
		}
	}
	if nameCol > 60 {
		nameCol = 60
	}

	for _, r := range rows {
		sublines := m.depScopeSublines(r)

		name := nameFor(r)
		line := "    · " + name
		// With no per-scope sublines (installed nowhere, nothing pending),
		// keep the steady state inline so the row isn't stateless.
		if len(sublines) == 0 {
			line += strings.Repeat(" ", max(0, nameCol-lipgloss.Width(name))) + "  " + renderState(r)
		}
		if t := requiredByTrail(r.requiredBy); t != "" {
			line += "  " + t
		}
		b.WriteString(truncate(line, m.width))
		b.WriteString("\n")
		for _, sl := range sublines {
			b.WriteString(truncate(sl, m.width))
			b.WriteString("\n")
		}
	}
}

// depScopeSublines builds the per-scope lines under a dep row: one per
// installed copy, with the targeted scope's line rewritten to the pending
// action, plus a synthetic "WILL INSTALL" line when the target scope has no
// copy yet. Returns nil when there's nothing to show per-scope (no installed
// copies and no pending action) so the caller can fall back to an inline
// state.
func (m model) depScopeSublines(r row) []string {
	action := m.depScopeAction(r)
	target := m.targetScope()

	var out []string
	for _, s := range r.scopes {
		if s.Scope == target && action != "" && action != "install" {
			out = append(out, m.renderDepActionSubrow(s.Scope, s.Version, action, r.version))
			continue
		}
		out = append(out, m.renderScopeSubrow(s, r.version))
	}
	if action == "install" {
		// Additive: the target scope has no copy yet.
		out = append(out, m.renderDepActionSubrow(target, "", "install", r.version))
	}
	return out
}

// renderDepActionSubrow renders one scope subline annotated with the pending
// install action. installedVer is the version currently on disk in that scope
// (empty for an additive install); pin is the version the action moves to.
func (m model) renderDepActionSubrow(scope, installedVer, action, pin string) string {
	var status string
	switch action {
	case "install":
		status = willInstallStyle.Render("WILL INSTALL")
		if v := formatVersion(pin); v != "" {
			status += " " + dimStyle.Render(v)
		}
	case "reinstall":
		status = installedStyle.Render("● " + dispVersion(installedVer))
		status += dimStyle.Render(" · ") + willInstallStyle.Render("WILL REINSTALL")
	case "upgrade":
		status = upgradeStyle.Render("△ " + dispVersion(installedVer))
		status += dimStyle.Render(" · ") + willUpgradeStyle.Render("WILL UPGRADE")
		if v := formatVersion(pin); v != "" {
			status += " " + dimStyle.Render("→ "+v)
		}
	default: // upgrade-available
		status = upgradeStyle.Render("△ " + dispVersion(installedVer))
		if v := formatVersion(pin); v != "" {
			status += dimStyle.Render(" (needs " + v + ")")
		}
		status += dimStyle.Render(" · ") + upgradeStyle.Render("UPGRADE AVAILABLE")
	}
	return fmt.Sprintf("        %-8s %s", scope, status)
}

// dispVersion formats a scope's on-disk version for a subrow, falling back to
// an em dash when unknown.
func dispVersion(v string) string {
	if f := formatVersion(v); f != "" {
		return f
	}
	return "—"
}

// rowStrings renders one picker row as terminal lines, flexing the label
// column to terminal width and wrapping the label to a second line when it
// would otherwise truncate. Continuation indents past the prefix so
// version/state stay aligned with the first-line layout.
//
//	prefix = "> [x] " or "    · " — printable + style escapes, fixed width
//	label  = item name
//	ver    = version string (right-padded to 18 cols when present)
//	detail = installed state / required-by trail
func rowStrings(prefix, label, ver, detail string, width int) []string {
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
		return []string{fmt.Sprintf("%s%-*s %-*s %s", prefix, labelCol, label, versionCol, ver, detail)}
	}

	// Two-line wrap: full label on line 1, version + detail aligned on line 2.
	indent := strings.Repeat(" ", prefixWidth)
	return []string{
		prefix + label,
		fmt.Sprintf("%s%-*s %-*s %s", indent, labelCol, "", versionCol, ver, detail),
	}
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

// renderState draws the row's install indicator. A row carrying per-scope
// installs renders one tag per copy — (status icon, scope, that copy's
// version) — mirroring the GUI's scope chips and this picker's dep sublines,
// so an item installed both globally and per-user reads as two tags:
//
//	● global v1.6.0 · ● user v1.6.0
//
// Scope-less rows (templates, blueprints, anything not installed) keep the
// single color-coded state: green dot installed, amber arrow for an
// available upgrade, muted hollow dot for not-installed.
func renderState(r row) string {
	if len(r.scopes) > 0 {
		return renderScopeTags(r.scopes)
	}
	icon := string(stateIcon(r.state))
	switch r.state {
	case "installed":
		return installedStyle.Render(icon + " installed")
	case "upgrade_available":
		// State of the world only: what's installed and what's required.
		// "needs vX" is the mismatch message; what a force-install would DO
		// about it is communicated separately (footer legend / toggles), not
		// fused into this glyph.
		msg := icon + " installed"
		if r.installedVersion != "" {
			msg += " " + formatVersion(r.installedVersion)
		}
		out := upgradeStyle.Render(msg)
		if r.version != "" {
			out += dimStyle.Render(" (needs " + formatVersion(r.version) + ")")
		}
		return out
	default: // not_installed / "" / unknown
		return dimStyle.Render(icon + " not installed")
	}
}

// renderScopeTags renders one tag per installed copy: the status icon, the
// scope it lives in, and the version on disk there. △ marks a copy whose
// per-scope state is stale against the pin; ● one that satisfies it.
func renderScopeTags(scopes []dto.ScopeInstallDTO) string {
	parts := make([]string, 0, len(scopes))
	for _, s := range scopes {
		style, icon := installedStyle, "●"
		if s.State == "upgrade_available" {
			style, icon = upgradeStyle, "△"
		}
		tag := style.Render(icon + " " + s.Scope)
		if v := formatVersion(s.Version); v != "" {
			tag += dimStyle.Render(" " + v)
		}
		parts = append(parts, tag)
	}
	return strings.Join(parts, dimStyle.Render(" · "))
}

// renderScopeSubrow renders one per-scope line under a scoped dependency row:
// the scope, the version installed there, and whether it satisfies the pin.
// △ + "(needs vX)" flags a stale scope; ● marks one that already matches.
func (m model) renderScopeSubrow(s dto.ScopeInstallDTO, pin string) string {
	ver := formatVersion(s.Version)
	if ver == "" {
		ver = "—"
	}
	var status string
	if s.State == "upgrade_available" {
		status = upgradeStyle.Render("△ " + ver)
		if pin != "" {
			status += dimStyle.Render(" (needs " + formatVersion(pin) + ")")
		}
	} else {
		status = installedStyle.Render("● " + ver)
	}
	return fmt.Sprintf("        %-8s %s", s.Scope, status)
}

// formatVersion prefixes "v" for semver-ish values (those starting with a
// digit), matching the GUI. Git refs, range operators, and empty strings
// pass through unchanged.
func formatVersion(v string) string {
	if v == "" {
		return v
	}
	if c := v[0]; c >= '0' && c <= '9' {
		return "v" + v
	}
	return v
}

// stateIcon is the row's status glyph: a filled dot for installed, an
// up-arrow for an available upgrade, a hollow dot for not installed (the
// default for unknown states too).
func stateIcon(state string) rune {
	switch state {
	case "installed":
		return '●'
	case "upgrade_available":
		return '↑'
	default:
		return '○'
	}
}

// requiredByTrail renders the pulling blueprints inline: the first picked
// parent plus a "+N" overflow count. Caller is responsible for trimming
// requiredBy to only the currently-picked parents (see readOnlyRows).
func requiredByTrail(requiredBy []string) string {
	if len(requiredBy) == 0 {
		return ""
	}
	s := "← " + requiredBy[0]
	if len(requiredBy) > 1 {
		s += fmt.Sprintf(" +%d", len(requiredBy)-1)
	}
	return s
}

// truncate clips s to width display cells, appending an ellipsis if it had to
// cut. It is ANSI-aware: lipgloss-styled segments embed escape sequences that
// occupy no width, so a naive rune slice would both miscount the budget and
// risk severing a sequence mid-stream — leaking broken codes and unreset color
// into the rest of the screen. width <= 0 returns s unchanged (early renders,
// before the first window-size message).
func truncate(s string, width int) string {
	if width <= 0 {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// wrapClamp word-wraps s to width and clamps the result to maxLines, marking
// an overflow by ellipsizing the last kept line.
func wrapClamp(s string, width, maxLines int) []string {
	if width <= 0 || maxLines <= 0 {
		return nil
	}
	lines := strings.Split(ansi.Wordwrap(s, width, ""), "\n")
	if len(lines) <= maxLines {
		return lines
	}
	lines = lines[:maxLines]
	lines[maxLines-1] = truncate(lines[maxLines-1]+"…", width)
	return lines
}

// searchBoxContentWidth is the inner width of the search-bar box for a given
// terminal width — about a third of the container, floored for small panes.
func searchBoxContentWidth(w int) int {
	return max(20, min(w-2, w/3))
}
