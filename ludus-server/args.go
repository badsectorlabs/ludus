package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
)

var (
	updateFlag        bool
	versionFlag       bool
	helpFlag          bool
	noAnsibleUpdate   bool
	debugFlag         bool
	migrationInfoFlag bool
	exportStatePath   string
	importStatePath   string
)

func init() {
	flag.BoolVar(&updateFlag, "update", false, "update the ludus install with this binary and embedded files and restart the ludus services")
	flag.BoolVar(&versionFlag, "v", false, "print the version of this ludus server")
	flag.BoolVar(&versionFlag, "version", false, "print the version of this ludus server")
	flag.BoolVar(&helpFlag, "h", false, "display help information")
	flag.BoolVar(&helpFlag, "help", false, "display help information")
	flag.BoolVar(&noAnsibleUpdate, "no-dep-update", false, "skip the dependency update check")
	flag.BoolVar(&debugFlag, "debug", false, "enable debug mode (can also be set with the LUDUS_DEBUG environment variable)")
	flag.BoolVar(&migrationInfoFlag, "migration-info", false, "print nonsecret legacy migration metadata without bootstrapping")
	flag.StringVar(&exportStatePath, "export-state", "", "export stopped legacy services' durable state to a new tar.gz archive")
	flag.StringVar(&importStatePath, "import-state", "", "import a migration archive into a stopped, unbootstrapped appliance")
	flag.Usage = printHelp
}

func checkArgs() {
	flag.Parse()

	if helpFlag {
		printHelp()
		os.Exit(0)
	}

	if versionFlag {
		fmt.Println(LudusVersion)
		os.Exit(0)
	}
	migrationModes := 0
	for _, enabled := range []bool{migrationInfoFlag, exportStatePath != "", importStatePath != "", updateFlag} {
		if enabled {
			migrationModes++
		}
	}
	if migrationModes > 1 {
		fmt.Fprintln(os.Stderr, "choose only one of --migration-info, --export-state, --import-state, or --update")
		os.Exit(1)
	}
	if migrationInfoFlag || exportStatePath != "" || importStatePath != "" {
		checkRoot()
		var err error
		if importStatePath != "" {
			err = importMigrationState(importStatePath)
		} else {
			err = exportMigrationState(exportStatePath, migrationInfoFlag)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if updateFlag {
		checkRoot()
		updateLudus()
		os.Exit(0)
	}

	if os.Getenv("LUDUS_DEBUG") == "1" || os.Getenv("LUDUS_DEBUG") == "true" {
		debugFlag = true
	}

	logLevel := slog.LevelInfo

	if debugFlag {
		logLevel = slog.LevelDebug
	}
	handler := NewPrettyHandler(os.Stderr, PrettyHandlerOptions{
		SlogOpts: slog.HandlerOptions{
			Level: logLevel,
		},
	})
	logger = slog.New(handler)
	slog.SetDefault(logger)
	slog.Debug("Debug mode enabled via flag or environment variable")
}

func printHelp() {
	fmt.Print(`
Ludus is a project to enable teams to quickly and
safely deploy test environments (ranges) to test tools and
techniques against representative virtual machines.

When run without arguments, ludus-server reads /opt/ludus/config.yml,
performs first-boot bootstrap against the configured Proxmox cluster
if not already complete, then serves the API.

Usage:
    ludus-server
    ludus-server --update

Flags:
`)
	flag.PrintDefaults()
}
