package main

import (
	"bytes"
	"log"
	"os"
	"os/exec"
	"strings"
)

// run a command in /bin/bash or /bin/sh (if /bin/bash doesn't exist) and optionally print the output
// if the command fails, and fatalFailure is set to true, it will exit the whole program
func Run(command string, printOutput bool, fatalFailure bool) string {
	shellBin := "/bin/bash"
	if _, err := os.Stat(shellBin); err != nil {
		if _, err = os.Stat("/bin/sh"); err != nil {
			log.Println("Could not find /bin/bash or /bin/sh")
			return "Could not find /bin/bash or /bin/sh"
		} else {
			shellBin = "/bin/sh"
		}
	}

	cmd := exec.Command(shellBin)
	cmd.Stdin = strings.NewReader(command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	var outputString string
	if out.String() == "" {
		outputString = "Command processed (no output)."
	} else {
		outputString = out.String()
	}
	if printOutput {
		log.Println(outputString)
	}
	if err != nil && fatalFailure {
		log.Printf("Fail error running %s: %s", command, err.Error())
		log.Fatal(err.Error())
	}
	return outputString
}
