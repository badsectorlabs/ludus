package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"

	"ludus/logger"
)

var GitCommitHash string

var LudusVersion string = "1.0.0+" + GitCommitHash

const (
	keyringService = "ludus-api"
)

var (
	cfgFile    string
	verbose    bool
	url        string
	proxy      string
	apiKey     string
	verify     bool
	jsonFormat bool
	userID     string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "ludus",
	Short: "An application to control Ludus",
	Long: `Ludus client v` + LudusVersion + `

Ludus is a CLI application to control a Ludus server
This application can manage users as well as ranges.

Ludus is a project to enable teams to quickly and
safely deploy test environments (ranges) to test tools and
techniques against representative virtual machines.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/ludus/config.yml)")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "verbose client output")
	rootCmd.PersistentFlags().StringVar(&url, "url", "https://198.51.100.1:8080", "Server Host URL")
	rootCmd.PersistentFlags().StringVar(&proxy, "proxy", "", "HTTP(S) Proxy URL")
	rootCmd.PersistentFlags().BoolVar(&verify, "verify", false, "verify the HTTPS certificate of the Ludus server")
	rootCmd.PersistentFlags().BoolVar(&jsonFormat, "json", false, "format output as json")
	rootCmd.PersistentFlags().StringVar(&userID, "user", "", "A user ID to impersonate (only available to admins)")
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	// bind the configuration to file/environment values
	// Without binding this flag we prevent it from being written to the persistent config file
	cobra.CheckErr(viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose")))
	cobra.CheckErr(viper.BindPFlag("json", rootCmd.PersistentFlags().Lookup("json")))
	cobra.CheckErr(viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url")))
	cobra.CheckErr(viper.BindPFlag("proxy", rootCmd.PersistentFlags().Lookup("proxy")))
	cobra.CheckErr(viper.BindPFlag("verify", rootCmd.PersistentFlags().Lookup("verify")))
	cobra.CheckErr(viper.BindPFlag("user", rootCmd.PersistentFlags().Lookup("user")))
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {

	// Find home directory.
	home, err := os.UserHomeDir()
	cobra.CheckErr(err)

	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {

		// Search in home directory with name ".ludus" (without extension).
		viper.AddConfigPath(fmt.Sprintf("%s/.config/ludus", home))
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	viper.SetEnvPrefix("LUDUS")
	viper.AutomaticEnv() // read in environment variables that match

	logger.InitLogger(verbose)

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		logger.Logger.Debug("Using config file:", viper.ConfigFileUsed())
	} else {
		logger.Logger.Debug("No config file found - using defaults")
	}
	logger.Logger.Debug("--- Configuration from cli and read from file ---")
	for s, i := range viper.AllSettings() {
		logger.Logger.Debug(fmt.Sprintf("\t%s = %s", s, i))
		// Not sure why this is required, but without it config values are overwritten by defaults
		if s == "url" {
			url = fmt.Sprintf("%s", i)
		} else if s == "proxy" {
			proxy = fmt.Sprintf("%s", i)
		}

	}
	logger.Logger.Debug("---")

	// Get the API key from an ENV var first (headless linux, SSH'd into a macOS machine)
	apiKey = os.Getenv("LUDUS_API_KEY")
	// If not in the ENV, try from keyring
	if len(apiKey) == 0 {
		apiKey, err = keyring.Get(keyringService, url)
	}
	// Fail if there is no API key if they aren't running the apikey or version subcommand
	if err != nil && !strings.Contains(strings.Join(os.Args, " "), " apikey") && !strings.Contains(strings.Join(os.Args, " "), " version") {
		logger.Logger.Fatalf(fmt.Sprintf("No Ludus API key found in system keyring for %s.\nSet one using the `apikey` command."+
			"\nYou can also set the LUDUS_API_KEY env variable if you are on a headless system.", url))
	} else {
		if len(apiKey) > 4 && strings.Contains(apiKey, ".") {
			logger.Logger.Debug("Got API key: " + strings.Split(apiKey, ".")[0] + ".***REDACTED***")
		} else {
			logger.Logger.Debug("No API key loaded from system keyring")
		}
	}

}
