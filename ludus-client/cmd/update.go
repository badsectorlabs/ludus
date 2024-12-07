package cmd

import (
	"context"
	"ludus/logger"
	"os"
	"runtime"
	"strings"

	"github.com/keygen-sh/keygen-go/v3"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:     "update",
	Short:   "Update the Ludus client or server",
	Long:    ``,
	Aliases: []string{"upgrade"},
}

var updateClientCmd = &cobra.Command{
	Use:   "client",
	Short: "Update this Ludus client",
	Long: `This command checks for an update to this Ludus client
and installs it if available.`,
	Run: func(cmd *cobra.Command, args []string) {

		keygen.APIURL = licenseURL
		keygen.APIVersion = licenseAPIVersion
		keygen.APIPrefix = licenseAPIPrefix
		keygen.UserAgent = "Ludus-Client/" + VersionString
		keygen.Account = licenseAccount
		keygen.Product = licenseProductClient
		keygen.LicenseKey = ""
		if verbose {
			keygen.Logger = keygen.NewLogger(keygen.LogLevelDebug)
		}

		logger.Logger.Infof("Current version: %s\n", VersionString)
		logger.Logger.Info("Checking for upgrades...")

		opts := keygen.UpgradeOptions{
			CurrentVersion: VersionString,
			Channel:        "stable",
			PublicKey:      licensePublicKey,
			Filename:       "{{.version}}%2F{{.program}}-client_{{if eq .platform \"darwin\"}}macOS{{else}}{{.platform}}{{end}}-{{.arch}}{{if .ext}}.{{.ext}}{{end}}",
			Constraint:     "1.0",
		}

		// Check for an upgrade
		upgradeContext := context.Background() // We don't need a timeout here
		release, err := keygen.Upgrade(upgradeContext, opts)
		logger.Logger.Debugf("Upgrade check result: %v", release)
		switch {
		case err == keygen.ErrUpgradeNotAvailable:
			logger.Logger.Info("No upgrade available, already at the latest version!")
			return
		case err != nil:
			logger.Logger.Errorf("Upgrade check failed! %v", err)
			return
		}
		logger.Logger.Infof("Upgrade available: %s", release.Version)

		// Install the upgrade
		if err := release.Install(upgradeContext); err != nil {
			logger.Logger.Errorf("Upgrade install failed: %v", err)
			if strings.Contains(err.Error(), "permission denied") {
				executablePath, err := os.Executable()
				if err != nil {
					logger.Logger.Debugf("Could not determine executable path: %v", err)
				} else {
					if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
						logger.Logger.Errorf("You may need to run this command with sudo to write to %s", executablePath)
					}
					if runtime.GOOS == "windows" {
						logger.Logger.Errorf("You may need to run this command with admin privileges to write to %s", executablePath)
					}
				}
			}
			if verbose {
				panic("Upgrade install failed!")
			}
			return
		}

		logger.Logger.Infof("Upgrade complete! Installed version %s.\n", release.Version)

	},
}

func init() {
	updateCmd.AddCommand(updateClientCmd)
	rootCmd.AddCommand(updateCmd)
}
