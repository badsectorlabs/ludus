package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/list"
	"github.com/go-ozzo/ozzo-validation/v4/is"
	"golang.org/x/term"
	"gopkg.in/yaml.v2"

	ludusapi "ludusapi"
)

const maxWidth = 100

var (
	gold   = lipgloss.AdaptiveColor{Light: "#D8AE2D", Dark: "#BF9000"}
	green  = lipgloss.AdaptiveColor{Light: "#02BA84", Dark: "#02BF87"}
	red    = lipgloss.AdaptiveColor{Light: "#A70000", Dark: "#A70000"}
	config ludusapi.Configuration

	finalConfirm              = false
	shouldShowAdminPortExpose = false
	initialAdmin              initialAdminConfig
	// initialAdminPasswordConfirm is only for interactive matching; it is not written to initial-admin.yml.
	initialAdminPasswordConfirm string
)

// initialAdminConfig holds the initial admin user details collected during interactive install.
type initialAdminConfig struct {
	Name     string `yaml:"name"`
	Email    string `yaml:"email"`
	UserID   string `yaml:"userID"`
	Password string `yaml:"password"`
}

// reservedUserIDs are not allowed for the initial admin (same as AddUser in ludus-api).
var reservedUserIDs = []string{"ADMIN", "ROOT", "CICD", "SHARED", "0"}

// initialAdminUserIDRegex matches valid user IDs: ^[A-Za-z0-9]{1,20}$
var initialAdminUserIDRegex = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)

// userExistsOnHostSystem checks if a user exists on the host (same logic as ludus-api).
// Used to validate the initial admin display name, since proxmoxUsername is derived from it.
func userExistsOnHostSystem(username string) bool {
	cmd := exec.Command("/usr/bin/id", username)
	return cmd.Run() == nil
}

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
	// Interactive field counts (Tab order): existing Proxmox includes 3 storage fields.
	numberOfFieldsWithStorage = 17 // 11 config + 5 initial admin (incl. password confirm) + 1 confirm
	numberOfFieldsFreshDebian = 14 // same minus vm pool, vm format, iso pool
	// Tab-order field indices when existingProxmox (storage on pages 2–3); see NewModel.
	idxSidebarVMPoolField   = 6
	idxSidebarVMFormatField = 7
	idxSidebarISOField      = 8
	// prevent_user_ansible_add and license_key field indices (Tab order).
	idxExistingPreventAnsibleField = 9
	idxExistingLicenseField        = 10
	idxFreshPreventAnsibleField    = 6
	idxFreshLicenseField           = 7
)

type Model struct {
	state             state
	lg                *lipgloss.Renderer
	styles            *Styles
	form              *huh.Form
	width             int
	currentFieldIndex int
	confirmed         bool
	fieldCount        int
	// Furthest field index reached via Tab/Shift+Tab; used to reveal sidebar lines only after the user passes each field.
	sidebarStorageMaxIdx int
}

// vmStoragePoolField returns either a datastore-filtered Select or the
// legacy free-text Input, depending on enumeration results. Same Key and
// bound Value in both cases so callers downstream are unchanged.
func vmStoragePoolField(vmStores []Datastore, hadEnumeration bool) huh.Field {
	if len(vmStores) > 0 {
		opts := make([]huh.Option[string], 0, len(vmStores))
		for _, s := range vmStores {
			opts = append(opts, huh.NewOption(labelFor(s), s.Name))
		}
		return huh.NewSelect[string]().
			Key("proxmox_vm_storage_pool").
			Title("What pool will store VMs?").
			Description("Filtered to datastores that allow VM images (use arrow keys to select)").
			Options(opts...).
			Value(&config.ProxmoxVMStoragePool)
	}
	desc := "The default is 'local'"
	if hadEnumeration {
		desc = "No active datastores found that accept VM images — enter a name manually"
	}
	return huh.NewInput().
		Key("proxmox_vm_storage_pool").
		Validate(func(s string) error {
			if s == "" {
				return fmt.Errorf("Proxmox VM Storage Pool cannot be empty")
			}
			return nil
		}).
		Title("What pool will store VMs?").
		Description(desc).
		Value(&config.ProxmoxVMStoragePool)
}

func isoStoragePoolField(isoStores []Datastore, hadEnumeration bool) huh.Field {
	if len(isoStores) > 0 {
		opts := make([]huh.Option[string], 0, len(isoStores))
		for _, s := range isoStores {
			opts = append(opts, huh.NewOption(labelFor(s), s.Name))
		}
		return huh.NewSelect[string]().
			Key("proxmox_iso_storage_pool").
			Title("What pool will store ISO files?").
			Description("Filtered to datastores that allow ISOs (use arrow keys to select)").
			Options(opts...).
			Value(&config.ProxmoxISOStoragePool)
	}
	desc := "The default is 'local'"
	if hadEnumeration {
		desc = "No active datastores found that accept ISOs — enter a name manually"
	}
	return huh.NewInput().
		Key("proxmox_iso_storage_pool").
		Validate(func(s string) error {
			if s == "" {
				return fmt.Errorf("Proxmox ISO Storage Pool cannot be empty")
			}
			return nil
		}).
		Title("What pool will store ISO files?").
		Description(desc).
		Value(&config.ProxmoxISOStoragePool)
}

// vmStorageFormatField returns a Select whose options reactively re-evaluate
// when config.ProxmoxVMStoragePool changes. Side effect: the callback
// pre-selects a type-appropriate default in config.ProxmoxVMStorageFormat
// (still user-overridable), so picking a zfspool auto-switches to "raw" etc.
// On fresh-install / enumeration-failure paths, returns the legacy free-text
// input so behavior matches today.
func vmStorageFormatField(vmStores []Datastore) huh.Field {
	if len(vmStores) == 0 {
		return huh.NewInput().
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
			Value(&config.ProxmoxVMStorageFormat)
	}

	// Lookup table for mutation inside OptionsFunc.
	byName := make(map[string]Datastore, len(vmStores))
	for _, s := range vmStores {
		byName[s.Name] = s
	}

	return huh.NewSelect[string]().
		Key("proxmox_vm_storage_format").
		Title("In what format should VMs be stored?").
		Description("Pre-selected from the VM pool's backend type; override if needed").
		// Rebinding to &config.ProxmoxVMStoragePool: huh re-runs this func
		// whenever the bound variable changes, letting us update both the
		// option list and (as a side-effect) the default selection.
		OptionsFunc(func() []huh.Option[string] {
			if pool, ok := byName[config.ProxmoxVMStoragePool]; ok {
				if derived := formatForPoolType(pool.Type); derived != "" {
					config.ProxmoxVMStorageFormat = derived
				}
			}
			return huh.NewOptions("qcow2", "raw", "vmdk")
		}, &config.ProxmoxVMStoragePool).
		Value(&config.ProxmoxVMStorageFormat)
}

func NewModel() Model {
	m := Model{width: maxWidth}
	m.lg = lipgloss.DefaultRenderer()
	m.styles = NewStyles(m.lg)
	m.confirmed = false

	if existingProxmox {
		m.fieldCount = numberOfFieldsWithStorage
	} else {
		m.fieldCount = numberOfFieldsFreshDebian
		config.ProxmoxVMStoragePool = "local"
		config.ProxmoxISOStoragePool = "local"
		config.ProxmoxVMStorageFormat = "qcow2"
	}

	var allStores []Datastore
	if existingProxmox {
		var enumErr error
		allStores, enumErr = enumerateDatastores()
		if enumErr != nil {
			log.Printf("warning: datastore enumeration failed: %v — falling back to manual entry", enumErr)
		}
	}
	vmStores := filterByContent(allStores, "images")
	isoStores := filterByContent(allStores, "iso")

	page2Fields := []huh.Field{
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
	}
	if existingProxmox {
		page2Fields = append(page2Fields,
			vmStoragePoolField(vmStores, allStores != nil),
			vmStorageFormatField(vmStores),
		)
	}

	page3Fields := []huh.Field{}
	if existingProxmox {
		page3Fields = append(page3Fields, isoStoragePoolField(isoStores, allStores != nil))
	}
	page3Fields = append(page3Fields,
		huh.NewConfirm().
			Key("prevent_user_ansible_add").
			Title("Should users be allowed to add ansible roles?").
			Description("Admins can always add ansible roles").
			Affirmative("Deny"). // Yea... this is backwards, but the field is named prevent_user_ansible_add...
			Negative("Allow").
			Value(&config.PreventUserAnsibleAdd),
	)

	m.form = huh.NewForm(
		// Page 1
		huh.NewGroup(
			huh.NewInput().
				Key("proxmox_node").
				Placeholder("ludus").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Proxmox node cannot be empty")
					} else if strings.Contains(s, " ") {
						return fmt.Errorf("Proxmox node cannot contain spaces")
					} else if strings.Contains(s, ".") {
						return fmt.Errorf("Proxmox node cannot contain dots, use only the base hostname, not the FQDN")
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
				Value(&config.ProxmoxPublicIP),
		),
		// Page 2
		huh.NewGroup(page2Fields...),
		// Page 3
		huh.NewGroup(page3Fields...),

		huh.NewGroup(
			huh.NewInput().
				Key("license_key").
				Title("Do you have a Ludus license key?").
				Description("Leave 'community' for community edition").
				Value(&config.LicenseKey),
		),

		huh.NewGroup(
			huh.NewInput().
				Key("initial_admin_name").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Initial admin name cannot be empty")
					}
					proxmoxUsername := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
					if userExistsOnHostSystem(proxmoxUsername) {
						return fmt.Errorf("username %s already exists on this host (derived from display name)", proxmoxUsername)
					}
					return nil
				}).
				Title("Initial admin display name").
				Description("e.g. John Doe - used for the first admin user").
				Value(&initialAdmin.Name),
			huh.NewInput().
				Key("initial_admin_email").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Initial admin email cannot be empty")
					}
					if err := is.EmailFormat.Validate(s); err != nil {
						return fmt.Errorf("Initial admin email must be a valid email address")
					}
					return nil
				}).
				Title("Initial admin email").
				Description("Used for web UI login").
				Value(&initialAdmin.Email),
			huh.NewInput().
				Key("initial_admin_userid").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("User ID cannot be empty")
					}
					if !initialAdminUserIDRegex.MatchString(s) {
						return fmt.Errorf("User ID must match ^[A-Za-z0-9]{1,20}$")
					}
					if slices.Contains(reservedUserIDs, strings.ToUpper(s)) {
						return fmt.Errorf("%s is a reserved user ID", s)
					}
					return nil
				}).
				Title("Initial admin user ID").
				Description("Short ID for CLI/API (e.g. JD). Letters and numbers only, 1-20 chars").
				Value(&initialAdmin.UserID),
			huh.NewInput().
				Key("initial_admin_password").
				Validate(func(s string) error {
					if len(s) < 8 {
						return fmt.Errorf("Password must be at least 8 characters long")
					}
					return nil
				}).
				Title("Initial admin password").
				Description("At least 8 characters - for web UI and Proxmox").
				Value(&initialAdmin.Password).
				EchoMode(huh.EchoModePassword),
			huh.NewInput().
				Key("initial_admin_password_confirm").
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("Please confirm the password")
					}
					if s != initialAdmin.Password {
						return fmt.Errorf("Passwords do not match")
					}
					return nil
				}).
				Title("Confirm initial admin password").
				Description("Must match the password above").
				Value(&initialAdminPasswordConfirm).
				EchoMode(huh.EchoModePassword),
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
	if existingProxmox {
		// Erase the wait line printed in runInteractiveInstall before the TUI draws.
		fmt.Fprint(os.Stdout, "\033[1A\033[2K")
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return m.form.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = min(msg.Width, maxWidth) - m.styles.Base.GetHorizontalFrameSize()
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			return m, tea.Quit
		case "down", "tab", "enter":
			m.currentFieldIndex = (m.currentFieldIndex + 1) % m.fieldCount
			m.sidebarStorageMaxIdx = max(m.sidebarStorageMaxIdx, m.currentFieldIndex)
		case "up", "shift+tab":
			m.currentFieldIndex--
			if m.currentFieldIndex < 0 {
				m.currentFieldIndex = m.fieldCount - 1
			}
			m.sidebarStorageMaxIdx = max(m.sidebarStorageMaxIdx, m.currentFieldIndex)
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
			proxmox_node      string
			proxmox_interface string
			proxmox_local_ip  string
			proxmox_public_ip string
			proxmox_gateway   string
			proxmox_netmask   string
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
		preventIdx := idxFreshPreventAnsibleField
		licenseIdx := idxFreshLicenseField
		if existingProxmox {
			preventIdx = idxExistingPreventAnsibleField
			licenseIdx = idxExistingLicenseField
		}

		// Form (left side)
		v := strings.TrimSuffix(m.form.View(), "\n\n")
		form := m.lg.NewStyle().Margin(1, 0).Render(v)

		// Status (right side)
		var status string
		{
			const statusWidth = 35
			statusMarginLeft := m.width - statusWidth - lipgloss.Width(form) - s.Status.GetMarginRight()
			storageBlock := ""
			if existingProxmox {
				var storageLines []string
				if m.sidebarStorageMaxIdx > idxSidebarVMPoolField && config.ProxmoxVMStoragePool != "" {
					storageLines = append(storageLines, "proxmox_vm_storage_pool: "+config.ProxmoxVMStoragePool)
				}
				if m.sidebarStorageMaxIdx > idxSidebarVMFormatField && config.ProxmoxVMStorageFormat != "" {
					storageLines = append(storageLines, "proxmox_vm_storage_format: "+config.ProxmoxVMStorageFormat)
				}
				if m.sidebarStorageMaxIdx > idxSidebarISOField && config.ProxmoxISOStoragePool != "" {
					storageLines = append(storageLines, "proxmox_iso_storage_pool: "+config.ProxmoxISOStoragePool)
				}
				if len(storageLines) > 0 {
					storageBlock = strings.Join(storageLines, "\n") + "\n"
				}
			}
			var tailLines []string
			if m.sidebarStorageMaxIdx > preventIdx {
				tailLines = append(tailLines, "prevent_user_ansible_add: "+strconv.FormatBool(m.form.GetBool("prevent_user_ansible_add")))
			}
			if m.sidebarStorageMaxIdx > licenseIdx && m.form.GetString("license_key") != "" {
				tailLines = append(tailLines, "license_key: "+m.form.GetString("license_key"))
			}
			tailBlock := ""
			if len(tailLines) > 0 {
				tailBlock = strings.Join(tailLines, "\n") + "\n"
			}
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
					storageBlock +
					tailBlock)
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
		showWarning("Only run Ludus install on a clean Debian machine that will be dedicated to Ludus",
			"I Understand", "Bail", 50, 11)
	} else {
		uglyWarning := `
    ~~~ You are installing Ludus on an existing Proxmox host ~~~
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
!!! Ludus will listen on 127.0.0.1:8081 and 0.0.0.0:8080 by default (configurable)       !!!
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!

Carefully consider the above block. If all items are compatible with your existing setup, you
may continue. Ludus comes with NO WARRANTY and no guarantee your existing setup will continue
to function. The Ludus install process will not reboot your host.
`

		showWarning(uglyWarning,
			"I have read and accept the above statement", "Bail", 93, 40)
	}

	showLicenseDialog()

	// Clear the terminal before showing the interactive installer
	fmt.Print("\033[H\033[2J")

	if fileExists(fmt.Sprintf("%s/config.yml", ludusInstallPath)) {
		loadConfig()
	} else {
		automatedConfigGenerator(false)
	}
	if existingProxmox {
		fmt.Fprintln(os.Stdout, "Enumerating existing data pools, please wait...")
		_ = os.Stdout.Sync()
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
	initialAdminPasswordConfirm = ""
	// Now that the form is done, write the config
	if !existingProxmox {
		config.ProxmoxVMStoragePool = "local"
		config.ProxmoxISOStoragePool = "local"
		config.ProxmoxVMStorageFormat = "qcow2"
	}
	config.ProxmoxHostname = config.ProxmoxNode
	writeConfigToFile(config, fmt.Sprintf("%s/config.yml", ludusInstallPath))

	// Write initial admin details for creation after ROOT is created in InitDb
	installDir := fmt.Sprintf("%s/install", ludusInstallPath)
	if err := os.MkdirAll(installDir, 0700); err != nil {
		fmt.Printf("Warning: could not create install dir: %v\n", err)
	} else {
		initialAdminPath := fmt.Sprintf("%s/initial-admin.yml", installDir)
		data, err := yaml.Marshal(&initialAdmin)
		if err != nil {
			fmt.Printf("Warning: could not marshal initial admin config: %v\n", err)
		} else if err := os.WriteFile(initialAdminPath, data, 0600); err != nil {
			fmt.Printf("Warning: could not write %s: %v\n", initialAdminPath, err)
		}
	}
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
	var l *list.List
	if !existingProxmox {
		l = list.New(
			fmt.Sprintf("%s install will cause the machine to reboot twice.", title),
			"Install will continue automatically after each reboot.",
			fmt.Sprintf("Check the progress of the install by running:\n'%s' from a root shell.", cmd),
		)
	} else {
		l = list.New(
			fmt.Sprintf("%s install has started.", title),
			fmt.Sprintf("Check the progress of the install by running:\n'%s' from a root shell.", cmd),
		)
	}
	startingText := lipgloss.NewStyle().
		MarginLeft(1).
		MarginRight(5).
		Padding(0, 1).
		Italic(true).
		Foreground(lipgloss.Color("#FFF7DB")).
		SetString("Starting install now!")

	return l.String() + "\n\n" + startingText.Render()
}
