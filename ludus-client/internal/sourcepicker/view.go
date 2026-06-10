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
	headerStyle    = lipgloss.NewStyle().Bold(true)
	cursorStyle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	dimStyle       = lipgloss.NewStyle().Faint(true)
	pickedStyle    = lipgloss.NewStyle().Foreground(accentColor)
	upgradeStyle   = lipgloss.NewStyle().Foreground(warnColor)
	implyStyle     = lipgloss.NewStyle().Foreground(accentColor)
	installedStyle = lipgloss.NewStyle().Foreground(installedColor)
	// Footer chip styles. The "on" color encodes severity: gold for the
	// benign scope flip (global), orange for the actually-risky
	// toggle (force overwrite) so the eye treats them differently. Off
	// stays a neutral grey for both.
	globalOnStyle = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	forceOnStyle  = lipgloss.NewStyle().Bold(true).Foreground(warnColor)
	offChipStyle  = lipgloss.NewStyle().Foreground(offColor)
	// Control-bar styles (the top hint + bottom footer). Keycaps are bold at
	// full intensity so they pop; the surrounding labels stay a readable muted
	// grey. Both sit above the faint legend, so the control bar reads as a
	// distinct band rather than blending into the contextual △ warnings.
	keyStyle     = lipgloss.NewStyle().Bold(true)
	controlStyle = lipgloss.NewStyle().Foreground(offColor)
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

	title := fmt.Sprintf("INSTALL from %s  %s",
		m.catalog.SourceName, m.headerCounts())
	top.WriteString(headerStyle.Render(truncate(title, m.width)))
	top.WriteString("\n")
	if desc := strings.TrimSpace(m.catalog.Description); desc != "" {
		top.WriteString(dimStyle.Render(truncate(desc, m.width)))
		top.WriteString("\n")
	}
	if meta := m.sourceMetaLine(); meta != "" {
		top.WriteString(dimStyle.Render(truncate(meta, m.width)))
		top.WriteString("\n")
	}
	top.WriteString("\n")

	if m.searching {
		top.WriteString(m.searchInput.View())
		top.WriteString("\n\n")
	} else if m.filter != "" {
		top.WriteString(dimStyle.Render(fmt.Sprintf("filter: %s", m.filter)))
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
	if m.hasConflictingPins() {
		legend := upgradeStyle.Render("△") +
			dimStyle.Render(" conflicting version pins across selected blueprints — ansible-galaxy will install only one; deselect a blueprint to disambiguate")
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
		chipFor(m.adv.Force, true, forceOnStyle)
	bottom.WriteString(chips)
	bottom.WriteString("\n")

	// The active panel gets whatever height the fixed blocks leave over.
	topStr, bottomStr := top.String(), bottom.String()
	panelHeight := m.height - strings.Count(topStr, "\n") - strings.Count(bottomStr, "\n")
	lines, cursorLine := m.panelLines()
	windowed := windowLines(lines, cursorLine, panelHeight)

	return topStr + strings.Join(windowed, "\n") + "\n" + bottomStr
}

// sourceMetaLine is the header's third line: "git · synced 2h ago" /
// "upload · uploaded just now". Pieces are skipped when the catalog doesn't
// carry them (older servers).
func (m model) sourceMetaLine() string {
	var parts []string
	if m.catalog.SourceType != "" {
		parts = append(parts, m.catalog.SourceType)
	}
	if when := relTime(m.catalog.LastSyncedAt); when != "" {
		verb := "synced"
		if m.catalog.SourceType == "upload" {
			verb = "uploaded"
		}
		parts = append(parts, verb+" "+when)
	}
	return strings.Join(parts, " · ")
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

// headerCounts renders the delta the current selection will apply:
// "(N to install · M to upgrade)". Empty pick reads "(nothing selected)".
func (m model) headerCounts() string {
	install, upgrade := m.selectionCounts()
	var parts []string
	if install > 0 {
		parts = append(parts, fmt.Sprintf("%d to install", install))
	}
	if upgrade > 0 {
		parts = append(parts, fmt.Sprintf("%d to upgrade", upgrade))
	}
	if len(parts) == 0 {
		return "(nothing selected)"
	}
	return "(" + strings.Join(parts, " · ") + ")"
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
// SELECTED blueprint has an installed version that doesn't match its pin (in
// any scope). readOnlyRows is already filtered to picked blueprints, so this
// only fires when the user's selection actually requires a version they don't
// have — not for stale deps of blueprints they haven't chosen.
func (m model) hasUpgradeMismatch() bool {
	target := m.targetScope()
	for _, kind := range []readOnlyKind{
		readOnlyGalaxyRoles,
		readOnlyGalaxyCollections,
		readOnlySubscriptionRoles,
	} {
		for _, r := range m.readOnlyRows(kind) {
			if len(r.scopes) > 0 {
				// Only the scope this install will write to matters: a stale
				// copy in a scope we won't touch (e.g. global is behind but
				// [g] is off, so install targets the up-to-date user path)
				// isn't something force would fix here.
				for _, s := range r.scopes {
					if s.Scope == target && s.State == "upgrade_available" {
						return true
					}
				}
			} else if r.state == "upgrade_available" {
				// Unscoped (collections) install to one place regardless of
				// the [g] toggle, so a stale one is always force-relevant.
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

	// Blueprint dependency sections are read-only: they install with their
	// blueprint and are removed via the ansible delete commands, not here.
	if m.activeTab == tabBlueprints {
		for _, ro := range []struct {
			title string
			kind  readOnlyKind
		}{
			{"Blueprint roles", readOnlyGalaxyRoles},
			{"Blueprint collections", readOnlyGalaxyCollections},
			{"Subscription roles", readOnlySubscriptionRoles},
		} {
			s := m.renderReadOnlySection(ro.title, ro.kind)
			if s == "" {
				continue
			}
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
		// Implied rows ([-]): a template/role pulled in by a checked blueprint.
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
			cursorLine = len(lines)
		}

		// State indicator first; append the "← by <blueprint>" trail for
		// implied rows so the user sees what pulled them in.
		detail := renderState(r)
		if isImplied && len(r.requiredBy) > 0 {
			detail += "  " + implyStyle.Render("← "+requiredByString(r.requiredBy))
		}

		// Layout per row: <cursor 2><checkbox 3><space><label><space><version 18><space><detail>
		// The label column flexes with terminal width; wraps to a second
		// line when even the flexed column can't fit the name.
		lines = append(lines, rowStrings(cursorMark+checkbox+" ", r.label, formatVersion(r.version), detail, m.width)...)

		// Show the focused row's description as a dim subtitle indented under
		// the label. Cursor-only keeps the list compact for long catalogs.
		if sec == m.active && i == cursorIdx && strings.TrimSpace(r.description) != "" {
			lines = append(lines, dimStyle.Render(truncate("      "+r.description, m.width)))
		}
	}
	return lines, cursorLine
}

func (m model) renderReadOnlySection(title string, kind readOnlyKind) string {
	rows := m.readOnlyRows(kind)
	if len(rows) == 0 {
		return ""
	}
	impliedCount := 0
	for _, r := range rows {
		if len(r.requiredBy) > 0 {
			impliedCount++
		}
	}
	// Align the state column by padding names to the widest in the section
	// (capped). Read-only rows pack onto one line — name, version, state,
	// then the "← by <blueprint>" trail — and truncate to width rather than
	// wrapping, since role names plus a parent trail easily exceed a narrow
	// terminal.
	nameCol := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.label); w > nameCol {
			nameCol = w
		}
	}
	if nameCol > 45 {
		nameCol = 45
	}

	heading := dimStyle.Render(fmt.Sprintf("─ %s (%d required) ─", title, impliedCount))
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for _, r := range rows {
		if len(r.scopes) > 0 {
			// Scoped role: a header line (name + required pin + trail), then a
			// subrow per scope showing that scope's version and whether it
			// matches — so "global stale / user fine" is legible at a glance.
			line := "    · " + fmt.Sprintf("%-*s", nameCol, r.label)
			if v := formatVersion(r.version); v != "" {
				if r.conflictingPins {
					v = upgradeStyle.Render("△ " + v)
				}
				line += "  " + dimStyle.Render("needs ") + v
			}
			if len(r.requiredBy) > 0 {
				line += "  " + implyStyle.Render("← "+requiredByString(r.requiredBy))
			}
			b.WriteString(truncate(line, m.width))
			b.WriteString("\n")
			for _, s := range r.scopes {
				b.WriteString(truncate(m.renderScopeSubrow(s, r.version), m.width))
				b.WriteString("\n")
			}
			continue
		}
		// Unscoped (collections): single inline line.
		line := "    · " + fmt.Sprintf("%-*s", nameCol, r.label)
		if v := formatVersion(r.version); v != "" {
			if r.conflictingPins {
				v = upgradeStyle.Render("△ " + v)
			}
			line += "  " + v
		}
		line += "  " + renderState(r)
		if len(r.requiredBy) > 0 {
			line += "  " + implyStyle.Render("← "+requiredByString(r.requiredBy))
		}
		b.WriteString(truncate(line, m.width))
		b.WriteString("\n")
	}
	return b.String()
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

// renderState draws the row's color-coded install indicator: a green dot for
// installed, an amber arrow for an available upgrade, a muted hollow dot for
// not-installed. Installed role rows also carry a scope tag — a CSV when the
// role lives in more than one scope (e.g. "global, user").
func renderState(r row) string {
	icon := string(stateIcon(r.state))
	scope := scopesLabel(r)
	switch r.state {
	case "installed":
		out := installedStyle.Render(icon + " installed")
		if scope != "" {
			out += dimStyle.Render(" " + scope)
		}
		return out
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
		if scope != "" {
			out += dimStyle.Render(" " + scope)
		}
		return out
	default: // not_installed / "" / unknown
		return dimStyle.Render(icon + " not installed")
	}
}

// scopesLabel joins an installed role's scope names into a CSV ("global",
// "user", or "global, user"). Empty for non-role and not-installed rows.
func scopesLabel(r row) string {
	names := make([]string, 0, len(r.scopes))
	for _, s := range r.scopes {
		names = append(names, s.Scope)
	}
	return strings.Join(names, ", ")
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

// selectionCounts summarizes what committing will do, for the header line.
// A checked item counts as an install or an upgrade by its current state
// (a checked already-installed item is a no-op).
func (m model) selectionCounts() (install, upgrade int) {
	classify := func(id, state string, picked map[string]struct{}) {
		if _, ok := picked[id]; !ok {
			return
		}
		switch state {
		case "upgrade_available":
			upgrade++
		case "installed":
			// no-op: already installed, checking it changes nothing
		default:
			install++
		}
	}
	bpSet := m.picked[sectionBlueprints.key()]
	for _, bp := range m.catalog.Blueprints.Items {
		classify(bp.ID, bp.State, bpSet)
	}
	tSet := m.picked[sectionTemplates.key()]
	for _, t := range m.catalog.Templates {
		classify(t.Name, t.State, tSet)
	}
	lrSet := m.picked[sectionLocalRoles.key()]
	for _, lr := range m.catalog.LocalRoles {
		classify(lr.Name, lr.State, lrSet)
	}
	return
}

// requiredByString renders the parent name(s) that pulled a read-only row
// in. Caller is responsible for trimming requiredBy to only the currently-
// picked parents (see readOnlyRows). With many parents, show the first
// two and tack on "+N" so long chains don't dominate the line.
func requiredByString(requiredBy []string) string {
	if len(requiredBy) == 0 {
		return ""
	}
	const maxVisible = 2
	if len(requiredBy) <= maxVisible {
		return strings.Join(requiredBy, ", ")
	}
	return fmt.Sprintf("%s +%d",
		strings.Join(requiredBy[:maxVisible], ", "), len(requiredBy)-maxVisible)
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
