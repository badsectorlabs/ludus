package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

const minimumUIWidth, minimumUIHeight = 54, 20

// Interactive input and output must not consume the piped bootstrap script or
// write a configuration (especially license keys) to the caller's stdout.
func openInstallerTTY() (*os.File, int, int, error) {
	if os.Getenv("TERM") == "" || os.Getenv("TERM") == "dumb" {
		return nil, 0, 0, fmt.Errorf("an interactive terminal is required; use --no-prompt with explicit settings")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("no controlling terminal; use --no-prompt with explicit settings: %w", err)
	}
	width, height, err := term.GetSize(int(tty.Fd()))
	if err != nil {
		tty.Close()
		return nil, 0, 0, fmt.Errorf("reading controlling terminal dimensions: %w", err)
	}
	return tty, width, height, nil
}

func terminalText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

func smallTerminalView() string {
	return "Terminal too small: resize to at least 54 x 20.\nEsc / Ctrl-C cancels; no settings saved."
}

type installerUIField struct {
	key, label, hint string
	group            int
}

const (
	uiEndpoints = iota
	uiVMID
	uiRootFSSize
	uiBridge
	uiVLANTag
	uiIP
	uiGateway
	uiNameserver
	uiWireGuardEndpoint
	uiWireGuardPort
	uiWireGuardForward
	uiStorage
	uiVMStorage
	uiVMStorageFormat
	uiISOStorage
	uiLicense
	uiCACertificate
	uiAdminName
	uiAdminEmail
	uiAdminUserID
	uiAdminPassword
	uiAdminPasswordConfirm
)

var installerUIFields = [...]installerUIField{
	uiEndpoints:            {"ENDPOINTS", "Proxmox endpoints", "Space-separated HTTPS URLs for the Proxmox nodes Ludus will manage.", 0},
	uiVMID:                 {"VMID", "LXC VMID", "Defaults to the next free VMID in this Proxmox cluster.", 0},
	uiRootFSSize:           {"LXC_ROOTFS_SIZE", "Rootfs size (GiB)", "Container disk size in GiB. The default is 250; ansible and packer files will be stored in the container.", 0},
	uiBridge:               {"LXC_BRIDGE", "Management bridge", "This interface must be able to reach the Proxmox endpoints from step 1", 1},
	uiVLANTag:              {"LXC_VLAN_TAG", "VLAN tag", "Optional: leave empty for untagged management traffic.", 1},
	uiIP:                   {"ETH0_IP", "Management IP", "Static IP address with CIDR, for example 172.31.253.2/24", 1},
	uiGateway:              {"ETH0_GW", "Gateway", "Gateway for the static management address, for example 172.31.253.1", 1},
	uiNameserver:           {"LXC_NAMESERVER", "DNS resolver", "Optional DNS server IP. Leave empty to inherit Proxmox DNS.", 2},
	uiWireGuardEndpoint:    {"WG_EP", "WireGuard endpoint", "Clients connect here. The installer persists host-to-container UDP forwarding unless disabled.", 2},
	uiWireGuardPort:        {"WG_PORT", "WireGuard UDP port", "The installer persists a forward for this UDP port unless disabled. Forward it on any upstream router as needed.", 2},
	uiWireGuardForward:     {"WG_FORWARD", "WireGuard forwarding", "Forward the WireGuard UDP port from this host to the container? Not needed if WireGuard users can reach the container IP directly.", 2},
	uiStorage:              {"STORAGE", "LXC rootfs storage", "Filtered to active datastores that allow container root filesystems.", 3},
	uiVMStorage:            {"VM_STORAGE", "VM storage", "Filtered to datastores that allow VM images (use arrow keys to select).", 3},
	uiVMStorageFormat:      {"VM_STORAGE_FORMAT", "VM disk format", "Pre-selected from the VM pool's backend type; override if needed.", 3},
	uiISOStorage:           {"ISO_STORAGE", "ISO storage", "Filtered to datastores that allow ISOs (use arrow keys to select).", 3},
	uiLicense:              {"LICENSE", "License key", "Leave 'community' for community edition.", 4},
	uiCACertificate:        {"CA_CERTIFICATE", "CA certificate", "Optional path to a PEM CA certificate to trust inside the container and templates.", 4},
	uiAdminName:            {"ADMIN_NAME", "Admin display name", "e.g. John Doe - used for the first admin user.", 5},
	uiAdminEmail:           {"ADMIN_EMAIL", "Admin email", "Used for web UI login.", 5},
	uiAdminUserID:          {"ADMIN_USERID", "Admin user ID", "Short ID for CLI/API (e.g. JD). Starts with a letter; letters and numbers only, up to 21 characters.", 5},
	uiAdminPassword:        {"ADMIN_PASSWORD", "Admin password", "At least 8 characters - for the web UI and Proxmox.", 5},
	uiAdminPasswordConfirm: {"ADMIN_PASSWORD_CONFIRM", "Confirm admin password", "Must match the password above.", 5},
}

const settingConfirmationDuration = 700 * time.Millisecond

type settingConfirmation struct {
	generation uint64
	active     bool
}

type settingConfirmationExpiredMsg struct {
	index      int
	generation uint64
}

type installerFormModel struct {
	options                             Options
	inventory                           *Inventory
	values                              [len(installerUIFields)]string
	settingConfirmations                [len(installerUIFields)]settingConfirmation
	network                             networkSuggestionState
	form                                *huh.Form
	inputs                              map[string]*huh.Input
	formatSelect, forwardSelect         *huh.Select[string]
	styles                              *Styles
	theme                               *huh.Styles
	width, height                       int
	sidebarScroll, reviewScroll, action int
	review, confirmed, cancelled        bool
	lightBackground                     bool
	// The forwarding notice explains host changes once, when Enabled is chosen.
	forwardNotice, forwardExplained bool
	noticeScroll                    int
	errorText                       string
	lastVMStorage                   string
}

func newInstallerForm(opts *Options, inv *Inventory, width, height int) (*installerFormModel, error) {
	// Bubble Tea reports the real background after Init; dark is its default.
	m := &installerFormModel{
		options: *opts, inventory: inv, styles: NewStyles(true), theme: customHuhTheme(true),
		width: width, height: height, inputs: make(map[string]*huh.Input),
		values: [len(installerUIFields)]string{
			uiEndpoints: opts.Endpoints, uiVMID: strconv.Itoa(opts.VMID),
			uiRootFSSize: strconv.Itoa(opts.RootFSSize),
			uiBridge:     opts.Bridge, uiVLANTag: opts.VLANTag, uiIP: opts.IP, uiGateway: opts.Gateway,
			uiNameserver: opts.Nameserver, uiWireGuardEndpoint: opts.WireGuardEndpoint,
			uiWireGuardPort: strconv.Itoa(opts.WireGuardPort), uiWireGuardForward: "yes",
			uiStorage: opts.Storage, uiVMStorage: opts.VMStorage,
			uiVMStorageFormat: opts.VMStorageFormat, uiISOStorage: opts.ISOStorage,
			uiLicense: opts.License, uiCACertificate: opts.CACertificate,
		},
	}
	if opts.NoWireGuardForward {
		m.values[uiWireGuardForward] = "no"
	}
	choices := make(map[string][]huh.Option[string])
	for _, pool := range []struct{ key, content string }{{"STORAGE", "rootdir"}, {"VM_STORAGE", "images"}, {"ISO_STORAGE", "iso"}} {
		for _, store := range storageChoices(inv, pool.content, pool.key == "ISO_STORAGE" && opts.ISODirectory != "") {
			label := fmt.Sprintf("%s (%s) - %.1f GiB free", terminalText(store.Name), terminalText(store.Type), float64(store.Available)/(1<<30))
			choices[pool.key] = append(choices[pool.key], huh.NewOption(label, store.Name))
		}
		if len(choices[pool.key]) == 0 {
			return nil, fmt.Errorf("no available %s storage choices (offline ISO storage must be shared)", pool.content)
		}
	}
	for _, bridge := range inv.Bridges {
		choices["LXC_BRIDGE"] = append(choices["LXC_BRIDGE"], huh.NewOption(terminalText(bridge), bridge))
	}
	if len(choices["LXC_BRIDGE"]) == 0 && !opts.MigrateHost {
		return nil, fmt.Errorf("no available management bridges")
	}
	choices["VM_STORAGE_FORMAT"] = m.formatChoices()
	choices["WG_FORWARD"] = []huh.Option[string]{huh.NewOption("Enabled", "yes"), huh.NewOption("Disabled", "no")}
	var grouped [6][]huh.Field
	for i, spec := range installerUIFields {
		if !m.adminRequired() && spec.group == installerUIFields[uiAdminName].group {
			continue
		}
		title, hint := spec.label, spec.hint
		if m.locked(spec.key) {
			title += " (read-only)"
			if spec.key == "LICENSE" {
				hint += " License supplied by file; read-only."
			} else {
				hint += " Preserved by the prepared migration plan; read-only."
			}
		}
		if spec.key == "ISO_STORAGE" && opts.ISODirectory != "" {
			hint = "Filtered to shared datastores that allow ISOs for offline installation (use arrow keys to select)."
		}
		// Forward navigation validates explicitly in Update. Huh also validates
		// on Blur, which would otherwise prevent backing out of an invalid page.
		var field huh.Field
		if options, ok := choices[spec.key]; ok && !m.locked(spec.key) {
			// Do not silently replace an explicit but unavailable setting. The
			// user can correct it via flags; discovery defaults are already valid.
			found := false
			for _, option := range options {
				found = found || option.Value == m.values[i]
			}
			if !found {
				return nil, fmt.Errorf("%s is not an available choice; correct the setting before continuing", spec.label)
			}
			selectField := huh.NewSelect[string]()
			selectField.Key(spec.key).Title(title).Description(hint).
				Options(options...).Value(&m.values[i]).Height(6)
			switch spec.key {
			case "VM_STORAGE_FORMAT":
				m.formatSelect = selectField
			case "WG_FORWARD":
				m.forwardSelect = selectField
			}
			field = selectField
		} else {
			input := huh.NewInput().Key(spec.key).Title(title).Description(hint).Value(&m.values[i])
			if spec.key == "LICENSE" && m.values[i] != "community" || spec.key == "ADMIN_PASSWORD" || spec.key == "ADMIN_PASSWORD_CONFIRM" {
				input.EchoMode(huh.EchoModePassword)
			}
			m.inputs[spec.key] = input
			field = input
		}
		grouped[spec.group] = append(grouped[spec.group], field)
	}
	titles := []string{"Container", "Network", "Access", "Storage", "License", "Admin user"}
	pages := len(grouped)
	if !m.adminRequired() {
		pages--
	}
	groups := make([]*huh.Group, 0, pages)
	for i, fields := range grouped[:pages] {
		groups = append(groups, huh.NewGroup(fields...).Title(fmt.Sprintf("%s (%d/%d)", titles[i], i+1, pages)))
	}
	m.form = huh.NewForm(groups...).WithTheme(huh.ThemeFunc(customHuhTheme)).WithShowHelp(false).WithShowErrors(false)
	m.lastVMStorage = m.values[uiVMStorage]
	m.initializeNetworkSuggestion()
	m.synchronize()
	m.resize()
	return m, nil
}

// adminRequired reports whether this install creates its first admin user.
// Migrations and state imports already carry their users.
func (m *installerFormModel) adminRequired() bool {
	return !m.options.MigrateHost && m.options.ImportDB == ""
}

func isAdminField(index int) bool { return index >= uiAdminName }

// lastField is the final field before review.
func (m *installerFormModel) lastField() int {
	if m.adminRequired() {
		return uiAdminPasswordConfirm
	}
	return uiCACertificate
}

func (m *installerFormModel) locked(key string) bool {
	if key == "LICENSE" && m.options.LicenseFile != "" {
		return true
	}
	if m.options.MigrateHost {
		switch key {
		case "LXC_BRIDGE", "LXC_VLAN_TAG", "ETH0_IP", "ETH0_GW", "WG_EP", "WG_PORT":
			return true
		}
	}
	return false
}

func (m *installerFormModel) candidate() Options {
	o := m.options
	o.Endpoints = m.values[uiEndpoints]
	o.VMID, _ = strconv.Atoi(m.values[uiVMID])
	o.RootFSSize, _ = strconv.Atoi(m.values[uiRootFSSize])
	o.Bridge, o.VLANTag = m.values[uiBridge], m.values[uiVLANTag]
	o.IP, o.Gateway = m.values[uiIP], m.values[uiGateway]
	o.Nameserver, o.WireGuardEndpoint = m.values[uiNameserver], m.values[uiWireGuardEndpoint]
	o.WireGuardPort, _ = strconv.Atoi(m.values[uiWireGuardPort])
	o.NoWireGuardForward = m.values[uiWireGuardForward] == "no"
	o.Storage, o.VMStorage = m.values[uiStorage], m.values[uiVMStorage]
	o.VMStorageFormat, o.ISOStorage = m.values[uiVMStorageFormat], m.values[uiISOStorage]
	o.License, o.CACertificate = m.values[uiLicense], m.values[uiCACertificate]
	if m.adminRequired() {
		o.AdminName, o.AdminEmail = m.values[uiAdminName], m.values[uiAdminEmail]
		o.AdminUserID, o.AdminPassword = m.values[uiAdminUserID], m.values[uiAdminPassword]
	}
	return o
}

func (m *installerFormModel) validate(index int, value string) error {
	o := m.candidate()
	if err := validateField(installerUIFields[index].key, value, &o, m.inventory); err != nil {
		if installerUIFields[index].key == "LICENSE" {
			return fmt.Errorf("License key: enter a license key or community")
		}
		return fmt.Errorf("%s: %w", installerUIFields[index].label, err)
	}
	return nil
}

func (m *installerFormModel) validateAll() error {
	for i, value := range m.values {
		if isAdminField(i) && !m.adminRequired() {
			continue
		}
		if err := m.validate(i, value); err != nil {
			return err
		}
	}
	o := m.candidate()
	return validateOptions(&o, m.inventory)
}

func (m *installerFormModel) formatChoices() []huh.Option[string] {
	for _, store := range m.inventory.Storages {
		if store.Name == m.values[uiVMStorage] && storageFormat(store.Type) == "qcow2" {
			return huh.NewOptions("qcow2", "raw")
		}
	}
	return huh.NewOptions("raw")
}

func (m *installerFormModel) synchronize() {
	m.synchronizeNetworkSelection()
	if m.lastVMStorage != m.values[uiVMStorage] {
		formats := m.formatChoices()
		valid := false
		for _, format := range formats {
			valid = valid || m.values[uiVMStorageFormat] == format.Value
		}
		if !valid {
			m.values[uiVMStorageFormat] = formats[0].Value
		}
		m.formatSelect.Options(formats...).Value(&m.values[uiVMStorageFormat])
		m.lastVMStorage = m.values[uiVMStorage]
	}
	if m.values[uiLicense] == "community" {
		m.inputs["LICENSE"].EchoMode(huh.EchoModeNormal)
	} else {
		m.inputs["LICENSE"].EchoMode(huh.EchoModePassword)
	}
}

func (m *installerFormModel) sidebarWidth() int {
	available := m.width - m.styles.Base.GetHorizontalFrameSize()
	frame := m.styles.Status.MarginLeft(1).GetHorizontalFrameSize()
	content := min(50, available-55-frame)
	if content < 44 {
		return 0
	}
	return content
}

func (m *installerFormModel) formWidth() int {
	width := m.width - m.styles.Base.GetHorizontalFrameSize()
	if sidebarWidth := m.sidebarWidth(); sidebarWidth > 0 {
		width -= sidebarWidth + m.styles.Status.MarginLeft(1).GetHorizontalFrameSize()
	}
	return max(20, width)
}

func (m *installerFormModel) resize() {
	m.form.WithWidth(m.formWidth()).WithHeight(max(8, m.height-8))
}

func (m *installerFormModel) Init() tea.Cmd {
	return tea.Batch(m.form.Init(), tea.RequestBackgroundColor)
}

func (m *installerFormModel) focusedIndex() int {
	key := m.form.GetFocusedField().GetKey()
	for i, spec := range installerUIFields {
		if spec.key == key {
			return i
		}
	}
	return 0
}

func (m *installerFormModel) flashSetting(index int) tea.Cmd {
	confirmation := &m.settingConfirmations[index]
	confirmation.generation++
	confirmation.active = true
	expiry := settingConfirmationExpiredMsg{index: index, generation: confirmation.generation}
	return tea.Tick(settingConfirmationDuration, func(time.Time) tea.Msg { return expiry })
}

// Huh queues group changes when NextField/PrevField crosses a boundary.
// Change groups synchronously so a burst of keys cannot queue the same
// transition repeatedly and skip validation or address discovery.
func (m *installerFormModel) moveField(index, direction int) tea.Cmd {
	target := index + direction
	if target < 0 || target >= len(installerUIFields) {
		return nil
	}
	if installerUIFields[target].group != installerUIFields[index].group {
		blur := m.form.GetFocusedField().Blur()
		if direction > 0 {
			return tea.Batch(blur, m.form.NextGroup())
		}
		return tea.Batch(blur, m.form.PrevGroup())
	}
	if direction > 0 {
		return m.form.NextField()
	}
	return m.form.PrevField()
}

func (m *installerFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case networkSuggestionMsg:
		m.acceptNetworkSuggestion(msg)
	case settingConfirmationExpiredMsg:
		confirmation := &m.settingConfirmations[msg.index]
		if confirmation.generation == msg.generation {
			confirmation.active = false
		}
		return m, nil
	case tea.BackgroundColorMsg:
		m.styles, m.lightBackground = NewStyles(msg.IsDark()), !msg.IsDark()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
	case tea.PasteMsg:
		if m.width < minimumUIWidth || m.height < minimumUIHeight || m.review || m.forwardNotice || !m.prepareEdit() {
			return m, nil
		}
	case tea.KeyPressMsg:
		if msg.String() == "esc" || msg.String() == "ctrl+c" {
			m.cancelNetworkSuggestion()
			m.cancelled = true
			return m, tea.Quit
		}
		if m.width < minimumUIWidth || m.height < minimumUIHeight {
			return m, nil
		}
		if m.review {
			return m.updateReview(msg)
		}
		if m.forwardNotice {
			return m.updateForwardNotice(msg)
		}
		index := m.focusedIndex()
		switch msg.String() {
		case "shift+tab":
			m.errorText = ""
			return m, m.moveField(index, -1)
		case "tab", "enter":
			m.synchronize()
			if index == uiIP && m.network.pending && m.values[uiIP] == "" {
				m.errorText = ""
				return m, nil
			}
			if err := m.validate(index, m.values[index]); err != nil {
				m.errorText = err.Error()
				return m, nil
			}
			m.errorText = ""
			if index == uiWireGuardForward && m.values[index] == "yes" && !m.forwardExplained {
				m.forwardNotice, m.action, m.noticeScroll = true, 0, 0
				return m, nil
			}
			if index == m.lastField() {
				if err := m.validateAll(); err != nil {
					m.errorText = err.Error()
					return m, nil
				}
				m.review, m.action, m.reviewScroll = true, 0, 0
				return m, tea.Batch(m.form.GetFocusedField().Blur(), m.flashSetting(index))
			}
			var suggestion tea.Cmd
			// The bridge starts a suggestion for the untagged network at once;
			// confirming a VLAN restarts it only if that changed the network.
			if index == uiBridge || index == uiVLANTag {
				suggestion = m.startNetworkSuggestion()
			}
			return m, tea.Batch(m.moveField(index, 1), m.flashSetting(index), suggestion)
		}
		if !m.prepareEdit() {
			return m, nil
		}
	}
	if m.review || m.forwardNotice {
		return m, nil
	}
	previousIP, previousGateway := m.values[uiIP], m.values[uiGateway]
	_, cmd := m.form.Update(msg)
	m.observeNetworkEdits(previousIP, previousGateway)
	m.synchronize()
	if m.form.State == huh.StateAborted {
		m.cancelled = true
		return m, tea.Quit
	}
	return m, cmd
}

// prepareEdit reports whether the focused field accepts typed or pasted input.
// Huh caches its group view during Update, so the license is masked before a
// key replacing "community" can render in plaintext.
func (m *installerFormModel) prepareEdit() bool {
	key := installerUIFields[m.focusedIndex()].key
	if m.locked(key) {
		return false
	}
	if key == "LICENSE" {
		m.inputs["LICENSE"].EchoMode(huh.EchoModePassword)
	}
	m.errorText = ""
	return true
}

func (m *installerFormModel) updateReview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "right":
		m.action = (m.action + 1) % 3
	case "shift+tab", "left":
		m.action = (m.action + 2) % 3
	case "down", "j":
		m.reviewScroll++
	case "up", "k":
		m.reviewScroll--
	case "pgdown":
		m.reviewScroll += m.reviewHeight()
	case "pgup":
		m.reviewScroll -= m.reviewHeight()
	case "home":
		m.reviewScroll = 0
	case "end":
		m.reviewScroll = len(m.settingLines(m.reviewWidth(), -1, false))
	case "enter", "space":
		switch m.action {
		case 0:
			m.review = false
			return m, m.form.GetFocusedField().Focus()
		case 1:
			m.cancelled = true
			return m, tea.Quit
		case 2:
			if err := m.validateAll(); err != nil {
				m.errorText, m.review = err.Error(), false
				return m, m.form.GetFocusedField().Focus()
			}
			m.confirmed = true
			return m, tea.Quit
		}
	}
	m.reviewScroll = max(0, min(m.reviewScroll, max(0, len(m.settingLines(m.reviewWidth(), -1, false))-m.reviewHeight())))
	return m, nil
}

func (m *installerFormModel) updateForwardNotice(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "right", "shift+tab", "left":
		m.action = 1 - m.action
	case "down", "j":
		m.noticeScroll++
	case "up", "k":
		m.noticeScroll--
	case "enter", "space":
		m.forwardNotice, m.forwardExplained = false, true
		if m.action == 1 {
			m.values[uiWireGuardForward] = "no"
			m.forwardSelect.Value(&m.values[uiWireGuardForward])
		}
		return m, tea.Batch(m.moveField(uiWireGuardForward, 1), m.flashSetting(uiWireGuardForward))
	}
	_, room, lines := m.forwardNoticeLayout()
	m.noticeScroll = max(0, min(m.noticeScroll, len(lines)-room))
	return m, nil
}

// forwardingExplanation states in plain terms what enabled forwarding
// changes on the Proxmox host and when it is unnecessary.
func (m *installerFormModel) forwardingExplanation() string {
	o := m.candidate()
	port, address := strconv.Itoa(o.WireGuardPort), strings.Split(o.IP, "/")[0]
	pkg := "iptables-persistent"
	if o.MigrateHost {
		pkg = "conntrack"
	}
	install := "• Install the " + pkg + " package on this host if it is missing."
	if o.Offline {
		install = "• Use the " + pkg + " package, which must already be installed on this host."
	}
	text := "WireGuard users connect to this Proxmox host on UDP port " + port + ". Keeping forwarding enabled will:\n\n" +
		install + "\n" +
		"• Add firewall rules that send UDP port " + port + " to the Ludus container at " + address + ".\n"
	if o.MigrateHost {
		return text + "• Restore those rules after reboots with the Ludus forwarding service.\n\n" +
			"After migration the container uses a private address on this host, so WireGuard users usually need this forward."
	}
	return text + "• Turn on IP forwarding and save the rules in /etc/iptables so they survive reboots. Your other firewall rules are kept.\n\n" +
		"You do not need this if the computers that will use WireGuard can already reach " + address +
		" directly. In that case, choose Disable and set the WireGuard endpoint to " + address + "."
}

func (m *installerFormModel) forwardNoticeLayout() (width, room int, lines []string) {
	width = min(72, m.reviewWidth()-4)
	for _, paragraph := range strings.Split(m.forwardingExplanation(), "\n") {
		text := lipgloss.NewStyle().Width(width).Render(paragraph)
		if item, ok := strings.CutPrefix(paragraph, "• "); ok {
			text = lipgloss.JoinHorizontal(lipgloss.Top, "• ", lipgloss.NewStyle().Width(width-2).Render(item))
		}
		lines = append(lines, strings.Split(text, "\n")...)
	}
	// Header and footer take three rows; the box adds borders, title, buttons, and help.
	return width, max(1, min(len(lines), m.height-10)), lines
}

func (m *installerFormModel) forwardNoticeView() string {
	width, room, lines := m.forwardNoticeLayout()
	offset := max(0, min(m.noticeScroll, len(lines)-room))
	content := strings.Join(lines[offset:offset+room], "\n")
	var buttons []string
	for i, label := range []string{"Keep enabled", "Disable"} {
		style := m.theme.Focused.BlurredButton
		if m.action == i {
			style = m.theme.Focused.FocusedButton
		}
		buttons = append(buttons, style.Render(label))
	}
	help := "Tab/←→ select; Enter continues"
	if len(lines) > room {
		help += "; ↑↓ scroll"
	}
	box := m.styles.Status.MarginTop(0).Padding(0, 1).Render(m.styles.StatusHeader.Render("What WireGuard forwarding changes") + "\n\n" +
		content + "\n\n" + strings.Join(buttons, "  ") + "\n" + m.styles.Help.Width(width).Render(help))
	return lipgloss.Place(m.reviewWidth(), max(1, m.height-3), lipgloss.Center, lipgloss.Center, box)
}

func (m *installerFormModel) shown(index int) string {
	value := m.values[index]
	if index == uiWireGuardForward {
		if value == "yes" {
			return "enabled"
		}
		return "disabled"
	}
	if (installerUIFields[index].key == "LICENSE" && value != "community" || index == uiAdminPassword) && value != "" {
		return "********"
	}
	if value == "" {
		return "(none)"
	}
	return terminalText(value)
}

// Wrapping styled labels and values together keeps ANSI sequences intact and
// follows rendered rows rather than assuming each setting occupies one line.
func (m *installerFormModel) settingLines(width, only int, live bool) []string {
	var lines []string
	for i, spec := range installerUIFields {
		if only >= 0 && only != i || i == uiAdminPasswordConfirm || isAdminField(i) && !m.adminRequired() {
			continue
		}
		// The live sidebar mutes values; the review page shows them at full
		// contrast because it is the last check before installation.
		labelStyle, valueStyle := lipgloss.NewStyle().Bold(true), lipgloss.NewStyle()
		if live {
			valueStyle = m.styles.Help
			if m.settingConfirmations[i].active {
				labelStyle = labelStyle.Foreground(m.styles.Gold)
				valueStyle = valueStyle.Foreground(m.styles.Gold)
			}
		}
		label := labelStyle.Render(spec.label + ":")
		value := valueStyle.Render(m.shown(i))
		line := lipgloss.NewStyle().Width(width).Render(label + " " + value)
		lines = append(lines, strings.Split(line, "\n")...)
	}
	return lines
}

func (m *installerFormModel) sidebar() string {
	contentWidth := m.sidebarWidth()
	room := max(1, m.height-9)
	lines := m.settingLines(contentWidth, -1, true)
	start, end := 0, 0
	for i := range installerUIFields {
		count := len(m.settingLines(contentWidth, i, true))
		if i == m.focusedIndex() {
			end = start + count
			break
		}
		start += count
	}
	if start < m.sidebarScroll {
		m.sidebarScroll = start
	} else if end > m.sidebarScroll+room {
		m.sidebarScroll = min(start, end-room)
	}
	m.sidebarScroll = max(0, min(m.sidebarScroll, max(0, len(lines)-room)))
	content := strings.Join(lines[m.sidebarScroll:min(len(lines), m.sidebarScroll+room)], "\n")
	return m.styles.Status.Width(contentWidth + m.styles.Status.GetHorizontalPadding()).Height(room + 1).MarginLeft(1).
		Render(m.styles.StatusHeader.Render("Live settings") + "\n" + content)
}

func (m *installerFormModel) reviewWidth() int {
	return max(20, m.width-m.styles.Base.GetHorizontalFrameSize()-2)
}
func (m *installerFormModel) reviewHeight() int { return max(1, m.height-11) }

func (m *installerFormModel) reviewView() string {
	width, room := m.reviewWidth(), m.reviewHeight()
	lines := m.settingLines(width, -1, false)
	offset := max(0, min(m.reviewScroll, max(0, len(lines)-room)))
	content := strings.Join(lines[offset:min(len(lines), offset+room)], "\n")
	content = lipgloss.NewStyle().Height(room).Render(content)
	var buttons []string
	for i, label := range []string{"Back", "Cancel", "Install"} {
		style := m.theme.Focused.BlurredButton
		if m.action == i {
			style = m.theme.Focused.FocusedButton
		}
		buttons = append(buttons, style.Render(label))
	}
	return m.styles.StatusHeader.Render("Review installation") + "\n\n" + content + "\n\n" +
		strings.Join(buttons, "  ") + "\n" + m.styles.Help.Width(width).Render("↑↓/PgUp/PgDn scroll; Tab/←→ select; Enter activates")
}

func (m *installerFormModel) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

func (m *installerFormModel) render() string {
	if m.confirmed || m.cancelled {
		return ""
	}
	if m.width < minimumUIWidth || m.height < minimumUIHeight {
		return smallTerminalView()
	}
	width := m.width - m.styles.Base.GetHorizontalFrameSize()
	header := "Ludus Interactive Installer / " + terminalText(m.options.Version)
	style, accent := m.styles.HeaderText, m.styles.Gold
	if m.errorText != "" {
		header, style, accent = terminalText(m.errorText), m.styles.ErrorHeaderText, m.styles.Red
	}
	rule := []lipgloss.WhitespaceOption{lipgloss.WithWhitespaceChars("/"), lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Foreground(accent))}
	// Keep the boundary a single line even for diagnostics with long paths.
	header = lipgloss.PlaceHorizontal(width, lipgloss.Left, style.MaxWidth(width).Render(header), rule...)
	var body, help string
	if m.review {
		body = m.reviewView()
	} else if m.forwardNotice {
		body = m.forwardNoticeView()
	} else {
		body = lipgloss.NewStyle().Width(m.formWidth()).MarginTop(1).Render(m.form.View())
		if m.sidebarWidth() > 0 {
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, m.sidebar())
		}
		help = m.styles.Help.Width(width).Render("Tab/Enter next; Shift+Tab back; arrows edit/choose") + "\n"
	}
	footer := lipgloss.PlaceHorizontal(width, lipgloss.Left, style.Render("Esc / Ctrl-C cancel"), rule...)
	return m.styles.Base.Render(header + "\n" + body + "\n" + help + footer)
}

// runInstallerForm returns whether the user confirmed the installation and
// whether the terminal reported a dark background, for later views.
func runInstallerForm(ctx context.Context, opts *Options, inv *Inventory) (confirmed, dark bool, err error) {
	tty, width, height, err := openInstallerTTY()
	if err != nil {
		return false, true, err
	}
	defer tty.Close()
	model, err := newInstallerForm(opts, inv, width, height)
	if err != nil {
		return false, true, err
	}
	model.network.ctx = ctx
	defer model.cancelNetworkSuggestion()
	result, err := tea.NewProgram(model, tea.WithInput(tty), tea.WithOutput(tty), tea.WithContext(ctx)).Run()
	if err != nil {
		return false, true, fmt.Errorf("installer form: %w", err)
	}
	final := result.(*installerFormModel)
	if !final.confirmed || final.cancelled {
		return false, !final.lightBackground, nil
	}
	*opts = final.candidate()
	return true, !final.lightBackground, nil
}
