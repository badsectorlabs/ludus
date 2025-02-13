package ludusapi

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
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
	licenseURL                     = "https://api.keygen.sh"
	licenseAPIVersion              = "1.7"
	licenseAPIPrefix               = "v1"
	licenseProductEnterprisePlugin = "f258d15f-4fab-47ca-839c-fc2a85f55b71"
	licenseAccount                 = "26f20308-539a-4d95-bdad-8edf70553cec"
	licensePublicKey               = "a3d9ac19af50b558b22e634531caddfa6a41bbeaee3685d796c02bbcd93aef59"
	binaryPublicKey                = "7990d22676174928335ce3b5eb96dd294b970fdb1427f9e4c0b84e9f8f9a9c50"
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
			if !FileExists(ludusInstallPath + "/plugins/enterprise/ludus-enterprise.so") {
				s.LicenseValid = false
				s.LicenseMessage = "Unable to connect to license server"
				return
			}
		}
		log.Printf("LICENSE: %v\n", err)
		return
	}
	if license.Expiry != nil {
		log.Printf("LICENSE: active, expires: %s, licensed to %s\n", license.Expiry.Format("2006-01-02 15:04:05"), license.Name)
		s.LicenseMessage = fmt.Sprintf("Active, expires: %s, licensed to %s", license.Expiry, license.Name)
	} else {
		log.Println("LICENSE: active, does not expire, licensed to", license.Name)
		s.LicenseMessage = fmt.Sprintf("Active, does not expire, licensed to %s", license.Name)
	}
	s.LicenseValid = true

	// Check for the enterprise plugin and load it if it exists
	var pluginsDir string
	if os.Geteuid() == 0 {
		pluginsDir = fmt.Sprintf("%s/plugins/enterprise/admin", ludusInstallPath)
	} else {
		pluginsDir = fmt.Sprintf("%s/plugins/enterprise", ludusInstallPath)
	}
	enterpriseLoaded := false
	// Always load the enterprise plugin if it exists first
	if FileExists(pluginsDir + "/ludus-enterprise.so") {
		err = s.LoadPlugin(pluginsDir + "/ludus-enterprise.so")
		if err != nil {
			log.Printf("LICENSE: error loading enterprise plugin: %v", err)
			log.Println("LICENSE: pulling compatible plugin from server (version: " + s.Version + ")")
			// Pull down the enterprise plugin since we have a valid license, perhaps we had a old version
			err = PullPlugin(fmt.Sprintf("/artifacts/ludus-enterprise_%s.so", s.VersionString), "ludus-enterprise.so", pluginsDir, s.Version, s.LicenseKey)
			if err != nil {
				log.Printf("LICENSE: error getting enterprise plugin: %v", err)
			}
		} else {
			enterpriseLoaded = true
		}
	} else {
		log.Println("LICENSE: no enterprise plugin found, pulling compatible plugin from server")
		err = PullPlugin(fmt.Sprintf("/artifacts/ludus-enterprise_%s.so", s.VersionString), "ludus-enterprise.so", pluginsDir, s.Version, s.LicenseKey)
		if err != nil {
			log.Printf("LICENSE: error getting enterprise plugin: %v", err)
		}
	}
	if !enterpriseLoaded {
		err = s.LoadPlugin(pluginsDir + "/ludus-enterprise.so")
		if err != nil {
			log.Printf("LICENSE: error loading enterprise plugin: %v", err)
		}
	}

	// Additional plugins are loaded by the enterprise plugin.

	// The server will initialize plugins in the main function
	// s.InitializePlugins()
}

func PullPlugin(path string, fileName string, pluginDir string, version string, licenseKey string) error {
	// Check for a .local-testing file in the plugin directory
	if _, err := os.Stat(pluginDir + "/.local-testing"); err == nil {
		log.Printf("LICENSE: In local-testing mode (%s/.local-testing exists), skipping plugin download\n", pluginDir)
		return nil
	}

	client := keygen.NewClientWithOptions(&keygen.ClientOptions{
		Account:    licenseAccount,
		APIURL:     licenseURL,
		PublicKey:  licensePublicKey,
		APIPrefix:  licenseAPIPrefix,
		APIVersion: licenseAPIVersion,
		UserAgent:  "Ludus-Server/" + version,
		LicenseKey: licenseKey,
	})
	ctx := context.Background()
	// Uncomment this to debug the plugin downloads
	// keygen.Logger = keygen.NewLogger(keygen.LogLevelDebug)

	artifact := &keygen.Artifact{}
	response, err := client.Get(ctx, path, nil, artifact)
	if err != nil {
		log.Printf("LICENSE: unable to download plugin %s: %v", fileName, err)
		return err
	}
	artifact.URL = response.Headers.Get("Location")
	// Write the enterprise plugin to disk
	if !FileExists(pluginDir) {
		err := os.MkdirAll(pluginDir, 0755)
		if err != nil {
			log.Printf("LICENSE: unable to create plugins directory: %v", err)
			return err
		}
	}
	pluginPath := filepath.Join(pluginDir, fileName)
	pluginFile, err := os.Create(pluginPath)
	if err != nil {
		log.Printf("LICENSE: unable to create plugin file %s: %v", fileName, err)
		return err
	}
	defer pluginFile.Close()

	// Download the actual plugin binary
	pluginResp, err := http.Get(artifact.URL)
	if err != nil {
		log.Printf("LICENSE: unable to download plugin binary %s: %v", fileName, err)
		return err
	}
	defer pluginResp.Body.Close()

	// Copy the plugin binary to the file
	_, err = io.Copy(pluginFile, pluginResp.Body)
	if err != nil {
		log.Printf("LICENSE: unable to write %s plugin: %v", fileName, err)
		return err
	}

	// Verify the signature
	if err := VerifySignature(pluginPath, artifact.Signature, binaryPublicKey, licenseProductEnterprisePlugin); err != nil {
		log.Printf("LICENSE: unable to verify signature for %s plugin: %v", fileName, err)
		return err
	}
	log.Printf("LICENSE: successfully verified signature for %s plugin", fileName)

	return nil
}

func VerifySignature(filePath string, signatureString string, publicKeyHex string, context string) error {

	signature, err := base64.RawStdEncoding.DecodeString(signatureString)
	if err != nil {
		return err
	}

	// Read and hash the file content
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create SHA-512 hash of file contents
	h := crypto.SHA512.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	checksum := h.Sum(nil)

	// Decode the public key from hex
	publicKey, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return errors.New("failed to decode ed25519ph public key")
	}

	// Verify public key length
	if l := len(publicKey); l != ed25519.PublicKeySize {
		return errors.New("invalid ed25519ph public key")
	}

	// Set up verification options with context
	opts := &ed25519.Options{
		Hash:    crypto.SHA512,
		Context: context,
	}

	// Verify the signature
	err = ed25519.VerifyWithOptions(publicKey, checksum, signature, opts)
	if err != nil {
		return fmt.Errorf("failed to verify ed25519ph signature: %v", err)
	}

	return nil
}
