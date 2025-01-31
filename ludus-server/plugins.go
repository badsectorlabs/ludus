package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/fsnotify.v1"
)

func watchPluginDirectory() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Create directory if it doesn't exist
	pluginDir := "/opt/ludus/plugins/enterprise/admin"
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return err
	}

	// Start watching directory
	if err := watcher.Add(pluginDir); err != nil {
		return err
	}

	// Debounce timer to prevent multiple rapid restarts
	var debounceTimer *time.Timer
	debounceDelay := 2 * time.Second

	log.Printf("Watching directory: %s", pluginDir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// Check if event is for .so file
			if filepath.Ext(event.Name) == ".so" &&
				(event.Op&fsnotify.Write == fsnotify.Write ||
					event.Op&fsnotify.Create == fsnotify.Create) {

				if debounceTimer != nil {
					debounceTimer.Stop()
				}

				debounceTimer = time.AfterFunc(debounceDelay, func() {
					log.Printf("Plugin change detected: %s", event.Name)
					log.Println("Restarting Ludus server...")
					os.Exit(0)
				})
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("Watch error: %v", err)
		}
	}
}
