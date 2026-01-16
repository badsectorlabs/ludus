package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"ludus/logger"
	"ludus/rest"
	"os"
	"strconv"
	"strings"

	"ludusapi/dto"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	userName        string
	newUserID       string
	email           string
	userIsAdmin     bool
	proxmoxPassword string
	password        string
	deleteRange     bool
)

// readPasswordWithAsterisks reads a password from stdin, displaying asterisks for each character typed
func readPasswordWithAsterisks() (string, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}

	var password strings.Builder
	reader := bufio.NewReader(os.Stdin)
	newlinePrinted := false

	// Ensure terminal is restored and newline is printed on exit
	defer func() {
		term.Restore(fd, oldState)
		if !newlinePrinted && password.Len() > 0 {
			fmt.Println()
		}
	}()

	for {
		char, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				// EOF reached - will be handled by defer
				break
			}
			return "", err
		}

		// Handle Enter/Return (CR or LF)
		if char == '\r' || char == '\n' {
			// Restore terminal state before printing newline
			term.Restore(fd, oldState)
			fmt.Println()
			newlinePrinted = true
			break
		}

		// Handle backspace/delete
		if char == 127 || char == 8 { // DEL or BS
			if password.Len() > 0 {
				// Remove last character from password
				currentPassword := password.String()
				password.Reset()
				password.WriteString(currentPassword[:len(currentPassword)-1])
				// Move cursor back, print space, move cursor back again
				fmt.Print("\b \b")
			}
			continue
		}

		// Handle Ctrl+C
		if char == 3 {
			term.Restore(fd, oldState)
			fmt.Println() // Print newline even on interrupt
			newlinePrinted = true
			return "", fmt.Errorf("interrupted")
		}

		// Regular character - add to password and print asterisk
		password.WriteByte(char)
		fmt.Print("*")
	}

	return password.String(), nil
}

// usersCmd represents the users command
var usersCmd = &cobra.Command{
	Use:     "users",
	Short:   "Perform actions related to users",
	Long:    ``,
	Aliases: []string{"user"},
}

var usersListCmd = &cobra.Command{
	Use:   "list [all]",
	Short: "List information about a user (alias: status)",
	Long: `Optionally supply the value "all" to retrieve
	information about all users.`,
	Args:    cobra.RangeArgs(0, 1),
	Aliases: []string{"status"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		if len(args) == 1 && args[0] == "all" {
			responseJSON, success = rest.GenericGet(client, "/user/all")
		} else if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/user?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/user")
		}
		if !success {
			return
		}
		var userObjectArray []UserObject
		err := json.Unmarshal(responseJSON, &userObjectArray)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Name", "userID", "Created", "Last Active", "Admin"})

		// Add data to table
		for _, item := range userObjectArray {
			created := formatTimeObject(item.DateCreated, "2006-01-02 15:04")
			active := formatTimeObject(item.DateLastActive, "2006-01-02 15:04")
			table.Append([]string{item.Name,
				item.UserID,
				created,
				active,
				strconv.FormatBool(item.IsAdmin)})
		}

		// Print table
		table.Render()

	},
}

var credsCmd = &cobra.Command{
	Use:   "creds",
	Short: "Perform actions related to Proxmox credentials",
	Long:  ``,
}

var usersCredsGetsCmd = &cobra.Command{
	Use:   "get",
	Short: "Get Proxmox credentials for a user",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/user/credentials"))
		if !success {
			return
		}

		// Unmarshal JSON data
		var data dto.GetCredentialsResponse
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Ludus Email", "Proxmox Username", "Proxmox Realm", "Proxmox/Ludus Password"})

		// Add data to table
		table.Append([]string{data.Result.LudusEmail,
			data.Result.ProxmoxUsername,
			data.Result.ProxmoxRealm,
			data.Result.ProxmoxPassword,
		})

		// Print table
		table.Render()

	},
}

var usersAPIKeyCmd = &cobra.Command{
	Use:   "apikey",
	Short: "Get a new Ludus apikey for a user",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if userID == "" {
			userID = strings.Split(apiKey, ".")[0]
		}

		if !noPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will create a new API key for user ID: %s !!!
         The old key will no longer work

Do you want to continue? (y/N): `, userID)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/user/apikey"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		// Unmarshal JSON data
		var data dto.GetAPIKeyResponse
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"UserID", "API Key"})

		// Add data to table
		table.Append([]string{data.Result.UserID,
			data.Result.ApiKey,
		})

		// Print table
		table.Render()

	},
}

func setupAPIKeyCmd(command *cobra.Command) {
	command.Flags().BoolVar(&noPrompt, "no-prompt", false, "skip the confirmation prompt")
}

var usersWireguardCmd = &cobra.Command{
	Use:   "wireguard",
	Short: "Get the Ludus wireguard configuration for a user",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/user/wireguard"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		// Unmarshal JSON data
		var data dto.GetWireguardConfigResponse
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Print(data.Result.WireGuardConfig)

	},
}

var usersAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a user to Ludus",
	Long:  ``,
	Args:  cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		// If no password provided via -p flag, prompt for it
	if password == "" {
			fmt.Print("Enter password for the user (leave empty to generate a random password): ")
			passwordInput, err := readPasswordWithAsterisks()
			if err != nil {
				logger.Logger.Fatal("Failed to read password: " + err.Error())
			}
			password = passwordInput
		}

		// Check that the password is at least 8 characters long if it is not blank
		if password != "" && len(password) < 8 {
			logger.Logger.Fatal("Password must be at least 8 characters long")
		}

		logger.Logger.Info("Adding user to Ludus, this can take up to a minute. Please wait.")

		requestBody := &dto.AddUserRequest{
			Name:     userName,
			Password: password,
			Email:    email,
			UserID:   newUserID,
			IsAdmin:  userIsAdmin,
		}
		responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/user"), requestBody)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		// Unmarshal JSON data
		var data dto.AddUserResponse
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"UserID", "Proxmox Username", "Admin", "API Key"})

		table.Append([]string{data.UserID,
			data.ProxmoxUsername,
			strconv.FormatBool(data.IsAdmin),
			data.ApiKey,
		})

		table.Render()

	},
}

func setupUsersAddCmd(command *cobra.Command) {
	command.Flags().StringVarP(&newUserID, "userid", "i", "", "the UserID of the new user (2-20 chars, typically capitalized initials)")
	command.Flags().StringVarP(&userName, "name", "n", "", "the name of the user (typically 'first last')")
	command.Flags().BoolVarP(&userIsAdmin, "admin", "a", false, "set this flag to make the user an admin of Ludus")
	command.Flags().StringVarP(&password, "password", "p", "", "the password for the user (must be at least 8 characters long, omit to prompt and generate a random password)")
	command.Flags().StringVarP(&email, "email", "e", "", "the email for the user")
	_ = command.MarkFlagRequired("email")
	_ = command.MarkFlagRequired("userid")
	_ = command.MarkFlagRequired("name")
}

var usersDeleteCmd = &cobra.Command{
	Use:     "rm",
	Short:   "Remove a user from Ludus",
	Long:    ``,
	Args:    cobra.ExactArgs(0),
	Aliases: []string{"remove", "delete"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		deleteURL := buildURLWithRangeAndUserID(fmt.Sprintf("/user/%s", newUserID))

		// If --delete-range flag is set, try to get the user's default range and confirm deletion
		if deleteRange {
			// Get the default range for the user being deleted, set the userID to the new userID to get the default range for the user being deleted
			originalUserID := userID
			userID = newUserID
			responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/user/default-range"))
			userID = originalUserID

			var defaultRangeID string
			if success {
				// Unmarshal JSON data
				var data dto.GetOrPostDefaultRangeIDResponse
				err := json.Unmarshal([]byte(responseJSON), &data)
				if err == nil {
					defaultRangeID = data.DefaultRangeID
				}
			}

			// Show warning and ask for confirmation
			var choice string
			if defaultRangeID != "" {
				logger.Logger.Warnf(`
!!! WARNING: If you continue the range %s and any VMs it contains will be permanently deleted !!!

Do you want to continue? (y/N): `, defaultRangeID)
			} else {
				logger.Logger.Fatalf("No default range found for user %s", newUserID)
			}
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}

			// Add deleteDefaultRange=true to the URL
			deleteURL = addQueryParameterToURL(deleteURL, "deleteDefaultRange", "true")
		}

		responseJSON, success = rest.GenericDelete(client, deleteURL)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupUsersDeleteCmd(command *cobra.Command) {
	command.Flags().StringVarP(&newUserID, "userid", "i", "", "the UserID of the user to remove")
	command.Flags().BoolVar(&deleteRange, "delete-range", false, "also delete the user's default range and any VMs it contains")
	_ = command.MarkFlagRequired("userid")
}

var usersCredsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set the proxmox password for a Ludus user",
	Long:  ``,
	Args:  cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if newUserID == "" {
			newUserID = strings.Split(apiKey, ".")[0]
		}

		requestBody := &dto.PostCredentialsRequest{
			UserID:          newUserID,
			ProxmoxPassword: proxmoxPassword,
		}
		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/user/credentials"), requestBody)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupUsersCredsSetCmd(command *cobra.Command) {
	command.Flags().StringVarP(&newUserID, "userid", "i", "", "the UserID of the user (default: the user ID of the API key)")
	command.Flags().StringVarP(&proxmoxPassword, "password", "p", "", "the proxmox password of the user")

	_ = command.MarkFlagRequired("password")
}

func init() {
	usersCmd.AddCommand(usersListCmd)
	setupAPIKeyCmd(usersAPIKeyCmd)
	usersCmd.AddCommand(usersAPIKeyCmd)
	usersCmd.AddCommand(usersWireguardCmd)
	setupUsersAddCmd(usersAddCmd)
	usersCmd.AddCommand(usersAddCmd)
	setupUsersDeleteCmd(usersDeleteCmd)
	usersCmd.AddCommand(usersDeleteCmd)
	credsCmd.AddCommand(usersCredsGetsCmd)
	setupUsersCredsSetCmd(usersCredsSetCmd)
	credsCmd.AddCommand(usersCredsSetCmd)
	usersCmd.AddCommand(credsCmd)
	rootCmd.AddCommand(usersCmd)
}
