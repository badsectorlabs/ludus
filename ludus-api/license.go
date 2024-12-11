package ludusapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/denisbrodbeck/machineid"
	"github.com/keygen-sh/keygen-go/v3"
)

const (
	licenseURL                     = "https://license.ludus.cloud"
	licenseAPIVersion              = "1.7"
	licenseAPIPrefix               = "v1"
	licenseProductEnterprisePlugin = "5722ca04-715d-4969-9130-a051532b7579"
	licenseAccount                 = "baaa4d02-5c5e-413d-8af1-f7846db1a838"
	licensePublicKey               = "7990d22676174928335ce3b5eb96dd294b970fdb1427f9e4c0b84e9f8f9a9c50"
)

func (s *Server) checkLicense() {
	keygen.Account = licenseAccount
	keygen.Product = licenseProductEnterprisePlugin
	keygen.LicenseKey = s.LicenseKey
	keygen.APIURL = licenseURL
	keygen.UserAgent = "Ludus-Server/" + s.Version

	fingerprint, err := machineid.ProtectedID(keygen.Product)
	if err != nil {
		log.Println("LICENSE: unable to get machine fingerprint:", err)
		s.LicenseValid = false
		s.LicenseMessage = "Unable to get machine fingerprint"
		return
	}
	ctx := context.Background()

	// Validate the license for the current fingerprint
	license, err := keygen.Validate(ctx, fingerprint)
	switch {
	case err == keygen.ErrLicenseNotActivated:
		// Activate the current fingerprint
		_, err := license.Activate(ctx, fingerprint)
		switch {
		case err == keygen.ErrMachineLimitExceeded:
			log.Println("LICENSE: machine limit has been exceeded!")
			s.LicenseValid = false
			s.LicenseMessage = "Machine limit has been exceeded"
			return
		case err != nil:
			log.Println("LICENSE: machine activation failed!")
			s.LicenseValid = false
			s.LicenseMessage = "Machine activation failed"
			return
		}
	case err == keygen.ErrLicenseExpired:
		log.Println("LICENSE: license is expired!")
		s.LicenseValid = false
		s.LicenseMessage = "License is expired"
		return
	case err != nil:
		var urlErr *url.Error
		if errors.As(err, &urlErr) || strings.Contains(err.Error(), "an error occurred") {
			log.Println("LICENSE: unable to connect to license server:", err)
			// If the enterprise plugin is not installed mark the license is not valid
			// The enterprise plugin can use a fallback on disk license if the network license fails
			if !fileExists(ludusInstallPath + "/plugins/ludus-enterprise.so") {
				s.LicenseValid = false
				s.LicenseMessage = "Unable to connect to license server"
				return
			}
		}
	}
	if license.Expiry != nil {
		log.Printf("LICENSE: active, expires: %s, licensed to %s\n", license.Expiry, license.Name)
		s.LicenseMessage = fmt.Sprintf("Active, expires: %s, licensed to %s", license.Expiry, license.Name)
	} else {
		log.Println("LICENSE: active, does not expire, licensed to", license.Name)
		s.LicenseMessage = fmt.Sprintf("Active, does not expire, licensed to %s", license.Name)
	}
	s.LicenseValid = true

	// Check for the enterprise plugin and load it if it exists
	var pluginsDir string
	if os.Geteuid() == 0 {
		// pluginsDir = fmt.Sprintf("%s/plugins/enterprise/admin", ludusInstallPath)
		return // Currently there is no enterprise plugin for the ludus-admin server
	} else {
		pluginsDir = fmt.Sprintf("%s/plugins/enterprise/", ludusInstallPath)
	}
	if fileExists(pluginsDir + "/ludus-enterprise.so") {
		err = s.LoadPlugin(pluginsDir + "/ludus-enterprise.so")
		if err != nil {
			log.Printf("LICENSE: error loading enterprise plugin: %v", err)
			log.Println("LICENSE: pulling compatible plugin from server (version: " + s.Version + ")")
			// Pull down the enterprise plugin since we have a valid license, perhaps we had a old version
			err = s.pullEnterprisePlugin()
			if err != nil {
				log.Printf("LICENSE: error getting enterprise plugin: %v", err)
			}
		}
	} else {
		log.Println("LICENSE: no enterprise plugin found, pulling compatible plugin from server")
		err = s.pullEnterprisePlugin()
		if err != nil {
			log.Printf("LICENSE: error getting enterprise plugin: %v", err)
		}
	}

	// Check license entitlements and pull down any plugins we are licensed to use
	err = s.pullEntitlements()
	if err != nil {
		log.Printf("LICENSE: error getting entitlements: %v", err)
	}

}

func (s *Server) pullEnterprisePlugin() error {
	client := keygen.NewClientWithOptions(&keygen.ClientOptions{
		Account: licenseAccount,
		APIURL:  licenseURL,
		// PublicKey:  licensePublicKey, // This fails to validate?
		APIPrefix:  licenseAPIPrefix,
		APIVersion: licenseAPIVersion,
		UserAgent:  "Ludus-Server/" + s.Version,
		LicenseKey: s.LicenseKey,
	})
	ctx := context.Background()

	response, err := client.Get(ctx, fmt.Sprintf("/artifacts/ludus-enterprise_%s.so", s.VersionString), nil, nil)
	if err != nil {
		log.Printf("LICENSE: unable to download enterprise plugin: %v", err)
		return err
	}
	// Write the enterprise plugin to disk
	pluginDir := filepath.Join(ludusInstallPath, "plugins", "enterprise")
	if !fileExists(pluginDir) {
		err := os.MkdirAll(pluginDir, 0755)
		if err != nil {
			log.Printf("LICENSE: unable to create plugins directory: %v", err)
			return err
		}
	}
	pluginPath := filepath.Join(pluginDir, "ludus-enterprise.so")
	pluginFile, err := os.Create(pluginPath)
	if err != nil {
		log.Printf("LICENSE: unable to create enterprise plugin file: %v", err)
		return err
	}
	defer pluginFile.Close()

	// Parse the JSON response to get the download URL
	var jsonResponse struct {
		Data struct {
			Links struct {
				Redirect string `json:"redirect"`
			} `json:"links"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(response.Body)).Decode(&jsonResponse); err != nil {
		log.Printf("LICENSE: unable to parse response JSON: %v", err)
		return err
	}

	// Download the actual plugin binary
	pluginResp, err := http.Get(jsonResponse.Data.Links.Redirect)
	if err != nil {
		log.Printf("LICENSE: unable to download plugin binary: %v", err)
		return err
	}
	defer pluginResp.Body.Close()

	// Copy the plugin binary to the file
	_, err = io.Copy(pluginFile, pluginResp.Body)
	if err != nil {
		log.Printf("LICENSE: unable to write enterprise plugin: %v", err)
		return err
	}

	// Load the enterprise plugin without restarting the server
	err = s.LoadPlugin(pluginPath)
	if err != nil {
		log.Printf("LICENSE: unable to load enterprise plugin: %v", err)
		return err
	}
	return nil
}

func (s *Server) pullEntitlements() error {
	return nil
}
