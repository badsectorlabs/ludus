package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/list"
	"golang.org/x/term"

	ludusapi "ludusapi"
)

const maxWidth = 100

var (
	gold   = lipgloss.AdaptiveColor{Light: "#D8AE2D", Dark: "#BF9000"}
	green  = lipgloss.AdaptiveColor{Light: "#02BA84", Dark: "#02BF87"}
	red    = lipgloss.AdaptiveColor{Light: "#A70000", Dark: "#A70000"}
	config ludusapi.Configuration

	finalConfirm           = false
	shouldShowAnsibleField = false
)

type Styles struct {
	Base,
	HeaderText,
	Status,
	StatusHeader,
	Highlight,
	ErrorHeaderText,
	Help lipgloss.Style
}

func NewStyles(lg *lipgloss.Renderer) *Styles {
	s := Styles{}
	s.Base = lg.NewStyle().
		Padding(1, 4, 0, 1)
	s.HeaderText = lg.NewStyle().
		Foreground(gold).
		Bold(true).
		Padding(0, 1, 0, 2)
	s.Status = lg.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(gold).
		PaddingLeft(1).
		MarginTop(1)
	s.StatusHeader = lg.NewStyle().
		Foreground(green).
		Bold(true)
	s.Highlight = lg.NewStyle().
		Foreground(gold)
	s.ErrorHeaderText = s.HeaderText.
		Foreground(red)
	s.Help = lg.NewStyle().
		Foreground(lipgloss.Color("240"))
	return &s
}

func customHuhTheme() *huh.Theme {
	t := huh.ThemeBase()

	t.Focused.Base = t.Focused.Base.BorderForeground(lipgloss.Color("8"))
	t.Focused.Title = t.Focused.Title.Foreground(lipgloss.Color("6"))
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(lipgloss.Color("6"))
	t.Focused.Directory = t.Focused.Directory.Foreground(lipgloss.Color("6"))
	t.Focused.Description = t.Focused.Description.Foreground(lipgloss.Color("8"))
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(lipgloss.Color("9"))
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(lipgloss.Color("9"))
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(lipgloss.Color("3"))
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(lipgloss.Color("3"))
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(lipgloss.Color("3"))
	t.Focused.Option = t.Focused.Option.Foreground(lipgloss.Color("7"))
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(lipgloss.Color("3"))
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(lipgloss.Color("2"))
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(lipgloss.Color("2"))
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(lipgloss.Color("7"))
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("7")).Background(lipgloss.Color("2"))
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(lipgloss.Color("7")).Background(lipgloss.Color("0"))

	t.Focused.TextInput.Cursor.Foreground(lipgloss.Color("5"))
	t.Focused.TextInput.Placeholder.Foreground(lipgloss.Color("8"))
	t.Focused.TextInput.Prompt.Foreground(lipgloss.Color("3"))

	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.NoteTitle = t.Blurred.NoteTitle.Foreground(lipgloss.Color("8"))
	t.Blurred.Title = t.Blurred.NoteTitle.Foreground(lipgloss.Color("8"))

	t.Blurred.TextInput.Prompt = t.Blurred.TextInput.Prompt.Foreground(lipgloss.Color("8"))
	t.Blurred.TextInput.Text = t.Blurred.TextInput.Text.Foreground(lipgloss.Color("7"))

	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	return t
}

type state int

const (
	statusNormal state = iota
	stateDone
	numberOfFields = 12 // 11 fields + 1 confirm, update this if you add any new fields
)

type Model struct {
	state             state
	lg                *lipgloss.Renderer
	styles            *Styles
	form              *huh.Form
	width             int
	currentFieldIndex int
	confirmed         bool
}

func NewModel() Model {
	m := Model{width: maxWidth}
	m.lg = lipgloss.DefaultRenderer()
	m.styles = NewStyles(m.lg)
	m.confirmed = false

	m.form = huh.NewForm(
		// Page 1
		huh.NewGroup(
			huh.NewInput().
				Key("proxmox_node").
				Placeholder("ludus").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox node cannot be empty")
					}
					return nil
				}).
				Title("Pick a name for this Ludus (Proxmox) node").
				Description("This will also be the hostname").
				Suggestions([]string{"ludus"}).
				Value(&config.ProxmoxNode),

			huh.NewInput().
				Key("proxmox_interface").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox Interface cannot be empty")
					}
					return nil
				}).
				Title("Which interface has internet connectivity?").
				Description("The suggestion is usually correct").
				Value(&config.ProxmoxInterface),

			huh.NewInput().
				Key("proxmox_local_ip").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox local IP cannot be empty")
					} else if net.ParseIP(s) == nil {
						return fmt.Errorf("Proxmox local IP is not a valid IP address")
					}
					return nil
				}).
				Title("What is the local IP of this host?").
				Description("The suggestion is usually correct").
				Value(&config.ProxmoxLocalIP),
			huh.NewInput().
				Key("proxmox_public_ip").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox Public IP cannot be empty")
					} else if net.ParseIP(s) == nil {
						return fmt.Errorf("Proxmox Public IP is not a valid IP address")
					}

					return nil
				}).
				Title("What is the public IP of this host").
				Description("This is used as the WireGuard endpoint").
				Suggestions([]string{"ludus"}).
				Value(&config.ProxmoxPublicIP),
		),
		// Page 2
		huh.NewGroup(
			huh.NewInput().
				Key("proxmox_gateway").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox gateway cannot be empty")
					} else if net.ParseIP(s) == nil {
						return fmt.Errorf("Proxmox gateway is not a valid IP address")
					}

					return nil
				}).
				Title("What is the gateway IP for this host?").
				Description("The suggestion is usually correct").
				Value(&config.ProxmoxGateway),

			huh.NewInput().
				Key("proxmox_netmask").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox netmask cannot be empty")
					} else if net.ParseIP(s) == nil {
						return fmt.Errorf("Proxmox netmask is not a valid IP address")
					}
					return nil
				}).
				Title(fmt.Sprintf("What is the netmask for interface %s", config.ProxmoxInterface)).
				Description("The suggestion is usually correct").
				Value(&config.ProxmoxNetmask),

			huh.NewInput().
				Key("proxmox_vm_storage_pool").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox VM Storage Pool cannot be empty")
					}
					return nil
				}).
				Title("What pool will store VMs").
				Description("The default is 'local'").
				Value(&config.ProxmoxVMStoragePool),

			huh.NewInput().
				Key("proxmox_vm_storage_format").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox VM Storage Format cannot be empty")
					} else if s != "qcow2" && s != "raw" && s != "vmdk" {
						return fmt.Errorf("Proxmox VM Storage Format must be 'qcow2', 'raw', or 'vmdk'")
					}
					return nil
				}).
				Title("In what format should VMs be stored?").
				Description("Use 'qcow2' for dir storage (local), 'raw' for ZFS").
				Value(&config.ProxmoxVMStorageFormat),
		),
		// Page 3

		huh.NewGroup(
			huh.NewInput().
				Key("proxmox_iso_storage_pool").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox ISO Storage Pool cannot be empty")
					}
					return nil
				}).
				Title("What pool will store ISO files?").
				Description("The default is 'local'").
				Value(&config.ProxmoxISOStoragePool),

			huh.NewInput().
				Key("ludus_nat_interface").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Ludus NAT Interface cannot be empty")
					}
					return nil
				}).
				Title(fmt.Sprintf("What new interface will be used to NAT out of %s", config.ProxmoxInterface)).
				Description("Default is 'vmbr1000' - don't change without a reason").
				Value(&config.LudusNATInterface),

			huh.NewConfirm().
				Key("prevent_user_ansible_add").
				Title("Should users be allowed to add ansible roles?").
				Description("Admins can always add ansible roles").
				Affirmative("Deny"). // Yea... this is backwards, but the field is named prevent_user_ansible_add...
				Negative("Allow").
				Value(&config.PreventUserAnsibleAdd),
		),

		huh.NewGroup(
			huh.NewInput().
				Key("license_key").
				Title("Do you have a Ludus license key?").
				Description("Leave blank for community edition").
				Value(&config.LicenseKey),
		),

		huh.NewGroup(
			huh.NewConfirm().
				Key("done").
				Title("Ready?").
				Validate(func(v bool) error {
					if !v {
						finalConfirm = true
						return fmt.Errorf("Go back with Shift+Tab")
					}
					return nil
				}).
				Affirmative("Install").
				Negative("Wait, no").
				Value(&finalConfirm),
		),
	).
		WithWidth(55).
		WithShowHelp(false).
		WithShowErrors(false).
		WithTheme(customHuhTheme())
	return m
}

func (m Model) Init() tea.Cmd {
	return m.form.Init()
}

func min(x, y int) int {
	if x > y {
		return y
	}
	return x
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = min(msg.Width, maxWidth) - m.styles.Base.GetHorizontalFrameSize()
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c", "q":
			return m, tea.Quit
		case "down", "tab", "enter":
			m.currentFieldIndex = (m.currentFieldIndex + 1) % numberOfFields
		case "up", "shift+tab":
			m.currentFieldIndex--
			if m.currentFieldIndex < 0 {
				m.currentFieldIndex = numberOfFields - 1
			}
		}

	}

	var cmds []tea.Cmd

	// Process the form
	form, cmd := m.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.form = f
		cmds = append(cmds, cmd)
	}

	if m.form.State == huh.StateCompleted {
		m.confirmed = true
		// Quit when the form is done.
		cmds = append(cmds, tea.Quit)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	s := m.styles

	switch m.form.State {
	case huh.StateCompleted:
		return s.Status.Margin(0, 1).Padding(1, 2).Width(60).Render(generateFinalMessage()) + "\n\n"
	default:

		var (
			proxmox_node              string
			proxmox_interface         string
			proxmox_local_ip          string
			proxmox_public_ip         string
			proxmox_gateway           string
			proxmox_netmask           string
			proxmox_vm_storage_pool   string
			proxmox_vm_storage_format string
			proxmox_iso_storage_pool  string
			ludus_nat_interface       string
			prevent_user_ansible_add  string
		)
		if m.form.GetString("proxmox_node") != "" {
			proxmox_node = "proxmox_node: " + m.form.GetString("proxmox_node")
		}
		if m.form.GetString("proxmox_interface") != "" {
			proxmox_interface = "proxmox_interface: " + m.form.GetString("proxmox_interface")
		}
		if m.form.GetString("proxmox_local_ip") != "" {
			proxmox_local_ip = "proxmox_local_ip: " + m.form.GetString("proxmox_local_ip")
		}
		if m.form.GetString("proxmox_public_ip") != "" {
			proxmox_public_ip = "proxmox_public_ip: " + m.form.GetString("proxmox_public_ip")
		}
		if m.form.GetString("proxmox_gateway") != "" {
			proxmox_gateway = "proxmox_gateway: " + m.form.GetString("proxmox_gateway")
		}
		if m.form.GetString("proxmox_netmask") != "" {
			proxmox_netmask = "proxmox_netmask: " + m.form.GetString("proxmox_netmask")
		}
		if m.form.GetString("proxmox_vm_storage_pool") != "" {
			proxmox_vm_storage_pool = "proxmox_vm_storage_pool: " + m.form.GetString("proxmox_vm_storage_pool")
		}
		if m.form.GetString("proxmox_vm_storage_format") != "" {
			proxmox_vm_storage_format = "proxmox_vm_storage_format: " + m.form.GetString("proxmox_vm_storage_format")
		}
		if m.form.GetString("proxmox_iso_storage_pool") != "" {
			proxmox_iso_storage_pool = "proxmox_iso_storage_pool: " + m.form.GetString("proxmox_iso_storage_pool")
		}
		if m.form.GetString("ludus_nat_interface") != "" {
			ludus_nat_interface = "ludus_nat_interface: " + m.form.GetString("ludus_nat_interface")
		}
		if m.currentFieldIndex >= 11 || shouldShowAnsibleField {
			prevent_user_ansible_add = "prevent_user_ansible_add: " + strconv.FormatBool(m.form.GetBool("prevent_user_ansible_add"))
			shouldShowAnsibleField = true
		}
		// Use this to debug the current field counter
		// prevent_user_ansible_add = fmt.Sprintf("Current field: %d", m.currentFieldIndex)

		// Form (left side)
		v := strings.TrimSuffix(m.form.View(), "\n\n")
		form := m.lg.NewStyle().Margin(1, 0).Render(v)

		// Status (right side)
		var status string
		{
			const statusWidth = 35
			statusMarginLeft := m.width - statusWidth - lipgloss.Width(form) - s.Status.GetMarginRight()
			status = s.Status.
				Height(lipgloss.Height(form)).
				Width(statusWidth).
				MarginLeft(statusMarginLeft).
				Render(s.StatusHeader.Render("Server Settings") + "\n" +
					proxmox_node + "\n" +
					proxmox_interface + "\n" +
					proxmox_local_ip + "\n" +
					proxmox_public_ip + "\n" +
					proxmox_gateway + "\n" +
					proxmox_netmask + "\n" +
					proxmox_vm_storage_pool + "\n" +
					proxmox_vm_storage_format + "\n" +
					proxmox_iso_storage_pool + "\n" +
					ludus_nat_interface + "\n" +
					prevent_user_ansible_add + "\n")
		}

		errors := m.form.Errors()
		header := m.appBoundaryView("Ludus Interactive Installer")
		if len(errors) > 0 {
			header = m.appErrorBoundaryView(m.errorView())
		}
		body := lipgloss.JoinHorizontal(lipgloss.Top, form, status)

		footer := m.appBoundaryView(m.form.Help().ShortHelpView(m.form.KeyBinds()))
		if len(errors) > 0 {
			footer = m.appErrorBoundaryView("")
		}

		return s.Base.Render(header + "\n" + body + "\n\n" + footer)
	}
}

func (m Model) errorView() string {
	var s string
	for _, err := range m.form.Errors() {
		s += err.Error()
	}
	return s
}

func (m Model) appBoundaryView(text string) string {
	return lipgloss.PlaceHorizontal(
		m.width,
		lipgloss.Left,
		m.styles.HeaderText.Render(text),
		lipgloss.WithWhitespaceChars("/"),
		lipgloss.WithWhitespaceForeground(gold),
	)
}

func (m Model) appErrorBoundaryView(text string) string {
	return lipgloss.PlaceHorizontal(
		m.width,
		lipgloss.Left,
		m.styles.ErrorHeaderText.Render(text),
		lipgloss.WithWhitespaceChars("/"),
		lipgloss.WithWhitespaceForeground(red),
	)
}

func runInteractiveInstall(existingProxmox bool) {

	physicalWidth, _, _ := term.GetSize(int(os.Stdout.Fd()))
	if physicalWidth < 100 {
		showWarning("The installer looks much better with a terminal at least 100 characters wide", "Continue", "Exit", 20, 15)
	}

	if !existingProxmox {
		showWarning("Only run Ludus install on a clean Debian 12 machine that will be dedicated to Ludus",
			"I Understand", "Bail", 50, 11)
	} else {
		uglyWarning := `
    ~~~ You are installing Ludus on an existing Proxmox 8 host ~~~
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
!!! Ludus will install: ansible, packer, dnsmasq, sshpass, curl, jq, iptables-persistent !!!
!!!                     gpg-agent, dbus, dbus-user-session, and vim                      !!!
!!! Ludus will install python packages: proxmoxer, requests, netaddr, pywinrm,           !!!
!!!                                     dnspython, and jmespath                          !!!
!!! Ludus will create the proxmox groups ludus_users and ludus_admins                    !!!
!!! Ludus will create the proxmox pools SHARED and ADMIN                                 !!!
!!! Ludus will create a wireguard server/interface 'wg0' with IP range 198.51.100.0/24   !!!
!!! Ludus will create an interface 'vmbr1000' with IP range 192.0.2.0/24 that NATs       !!!
!!! Ludus will create user ranges with IPs in the 10.0.0.0/16 network                    !!!
!!! Ludus will create user interfaces starting at vmbr1001 incrementing for each user    !!!
!!! Ludus will create the pam user 'ludus' and pam users for all Ludus users added       !!!
!!! Ludus will create the 'ludus-admin' and 'ludus' systemd services                     !!!
!!! Ludus will listen on 127.0.0.1:8081 and 0.0.0.0:8080                                 !!!
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!

Carefully consider the above block. If all items are compatible with your existing setup, you
may continue. Ludus comes with NO WARRANTY and no guarantee your existing setup will continue
to function. The Ludus install process will not reboot your host.
`

		showWarning(uglyWarning,
			"I have read and accept the above statement", "Bail", 93, 40)
	}

	if fileExists(fmt.Sprintf("%s/config.yml", ludusInstallPath)) {
		loadConfig()
	} else {
		automatedConfigGenerator(false)
	}
	finalModel, err := tea.NewProgram(NewModel()).Run()
	if err != nil {
		fmt.Println("Oh no:", err)
		os.Exit(1)
	}
	if !finalModel.(Model).confirmed {
		fmt.Println("Exiting")
		os.Exit(1)
	}
	// Now that the form is done, write the config
	config.ProxmoxHostname = config.ProxmoxNode
	writeConfigToFile(config, fmt.Sprintf("%s/config.yml", ludusInstallPath))
}

func generateFinalMessage() string {
	lg := lipgloss.DefaultRenderer()
	s := NewStyles(lg)
	title := "Ludus"
	title = s.Highlight.Render(title)
	cmdStyle := lipgloss.NewStyle().
		Bold(true).
		Underline(true).
		Foreground(gold)
	cmd := cmdStyle.Render("ludus-install-status")
	l := list.New(
		fmt.Sprintf("%s install will cause the machine to reboot twice.", title),
		"Install will continue automatically after each reboot.",
		fmt.Sprintf("Check the progress of the install by running:\n'%s' from a root shell.", cmd),
	)
	startingText := lipgloss.NewStyle().
		MarginLeft(1).
		MarginRight(5).
		Padding(0, 1).
		Italic(true).
		Foreground(lipgloss.Color("#FFF7DB")).
		SetString("Starting install now!")

	return l.String() + "\n\n" + startingText.Render()
}
