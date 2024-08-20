package main

import (
	"flag"
	"fmt"
	"os"
)

var (
	interactiveInstall bool
	updateFlag         bool
	versionFlag        bool
	helpFlag           bool
	noPromptFlag       bool
	nodeName           string
	autoGenerateConfig bool
)

func init() {
	flag.BoolVar(&updateFlag, "update", false, "update the ludus install with this binary and embedded files and restart the ludus services")
	flag.BoolVar(&noPromptFlag, "no-prompt", false, "run the installer without prompting for confirmation")
	flag.BoolVar(&versionFlag, "v", false, "print the version of this ludus server")
	flag.BoolVar(&versionFlag, "version", false, "print the version of this ludus server")
	flag.BoolVar(&helpFlag, "h", false, "display help information")
	flag.BoolVar(&helpFlag, "help", false, "display help information")
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

	if updateFlag {
		checkRoot()
		updateLudus()
		os.Exit(0)
	}

	interactiveInstall = !noPromptFlag
	autoGenerateConfig = noPromptFlag

	if noPromptFlag {
		if flag.NArg() > 0 {
			nodeName = flag.Arg(0)
		} else {
			nodeName = ""
		}
	}
}

func printHelp() {
	fmt.Print(`
Ludus is a project to enable teams to quickly and
safely deploy test environments (ranges) to test tools and
techniques against representative virtual machines.

When run without arguments, Ludus will check for a Ludus
install at /opt/ludus and prompt the user to install Ludus
if an existing install is not found.

When run with --no-prompt an optional node name can be provided as an
argument to set the proxmox node name in the configuration file.

Usage:
    ludus-server
    ludus-server --no-prompt [nodename]
    ludus-server --update

Flags:
`)
	flag.PrintDefaults()
}
