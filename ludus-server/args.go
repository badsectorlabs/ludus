package main

import (
	"fmt"
	"os"
)

func checkArgs() {
	if len(os.Args) > 1 && os.Args[1] == "--no-prompt" {
		interactiveInstall = false
	} else {
		interactiveInstall = true
	}
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
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
        --update       update the ludus install with this binary and 
                       embedded files and restart the ludus services
        --no-prompt    run the installer without prompting for confirmation
    -h, --help         help for ludus-server
    -v, --version      print the version of this ludus server
`)
		os.Exit(0)
	} else if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println(LudusVersion)
		os.Exit(0)
	} else if len(os.Args) > 1 && os.Args[1] == "--update" {
		// If the user wants to update, just do that and exit
		checkRoot()
		updateLudus()
		os.Exit(0)
	}
}
