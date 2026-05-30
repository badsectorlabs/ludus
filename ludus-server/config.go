package main

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v2"
)

// Load the config file from disk into the config struct
func loadConfig() {
	// Read the config file
	data, err := os.ReadFile(fmt.Sprintf("%s/config.yml", ludusInstallPath))
	if err != nil {
		log.Fatalf("Error opening config: %v", err)
	}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("Error unmarshalling config: %v", err)
	}
	if err := config.ApplyPortDefaultsAndValidate(); err != nil {
		log.Fatalf("%v", err)
	}
}
