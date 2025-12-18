package ludusapi

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"ludusapi/dto"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/denisbrodbeck/machineid"
	"github.com/keygen-sh/keygen-go/v3"
	"github.com/pocketbase/pocketbase/core"
)

const (
	licenseURL                              = "https://license.ludus.cloud"
	licenseAPIVersion                       = "1.7"
	licenseAPIPrefix                        = "v1"
	LicenseProductLudus                     = "5722ca04-715d-4969-9130-a051532b7579"
	licensePackageSubscriptionRoles         = "0d55c084-4181-4d13-8b05-a349b1409760"
	licenseProductSubscriptionRolesMetadata = "7b75d702-0448-4d82-9963-2b1f1f460022"
	LicensePackageLudusEnterprisePlugin     = "a8ecdfa4-6cf7-4a7c-93cc-95fe44c94d14"
	LicensePackageLudusAntisandboxPlugin    = "a335f37d-e603-405c-8c99-0bb3185a87e8"
	licenseAccount                          = "baaa4d02-5c5e-413d-8af1-f7846db1a838"
	licensePublicKey                        = "70cb26141f38840b8f3f499d4875a829a9d251bd3337278995832b9ea4e39d12"
	binaryPublicKey                         = "7990d22676174928335ce3b5eb96dd294b970fdb1427f9e4c0b84e9f8f9a9c50"
	ludusLicenseEnterprise                  = "enterprise"
	ludusLicenseCommunity                   = "community"
	ludusLicenseProfessional                = "professional"
)

func (s *Server) checkLicense() {
	keygen.Account = licenseAccount
	keygen.Product = LicenseProductLudus
	keygen.LicenseKey = s.LicenseKey
	keygen.APIURL = licenseURL
	keygen.UserAgent = "Ludus-Server/" + s.Version

	if os.Getenv("LUDUS_DEBUG_LICENSE") == "1" {
		keygen.Logger = keygen.NewLogger(keygen.LogLevelDebug)
	}

	fingerprint, err := machineid.ProtectedID(keygen.Product)
	if err != nil {
		log.Println("LICENSE: unable to get machine fingerprint:", err)
		s.LicenseValid = false
		s.LicenseMessage = "Unable to get machine fingerprint"
		return
	}
	ctx := context.Background()

	var pluginsDir string
	if os.Geteuid() == 0 {
		pluginsDir = fmt.Sprintf("%s/plugins/enterprise/admin", ludusInstallPath)
	} else {
		pluginsDir = fmt.Sprintf("%s/plugins/enterprise", ludusInstallPath)
	}
	enterpriseLoaded := false

	licenseCheckBucket := NewLeakyBucket(fmt.Sprintf("%s/install/.license-check-bucket", ludusInstallPath), 6, 0.02)
	if !licenseCheckBucket.Allow() {
		log.Println("LICENSE: license check bucket is full, skipping license check")
		return
	}

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
			log.Printf("LICENSE: machine activation failed: %v\n", err)
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
			} else {
				log.Println("LICENSE: enterprise plugin is present, attempting to load it")
				err = s.LoadPlugin(pluginsDir + "/ludus-enterprise.so")
				if err != nil {
					log.Printf("LICENSE: error loading enterprise plugin as part of network fallback: %v", err)
				} else {
					enterpriseLoaded = true
				}
			}
		}
		log.Printf("LICENSE: %v\n", err)
		return
	}
	if license.Expiry != nil {
		log.Printf("LICENSE: active, expires: %s, licensed to %s\n", license.Expiry.Format("2006-01-02 15:04:05"), license.Name)
		s.LicenseMessage = fmt.Sprintf("License active, expires: %s, licensed to %s", license.Expiry.Format("2006-01-02 15:04:05"), license.Name)
		s.LicenseName = license.Name
		s.LicenseExpiry = license.Expiry
	} else {
		log.Println("LICENSE: active, does not expire, licensed to", license.Name)
		s.LicenseMessage = fmt.Sprintf("License active, does not expire, licensed to %s", license.Name)
		s.LicenseName = license.Name
		s.LicenseExpiry = nil
	}
	s.LicenseValid = true

	// Always load the enterprise plugin if it exists first
	if FileExists(pluginsDir + "/ludus-enterprise.so") {
		err = s.LoadPlugin(pluginsDir + "/ludus-enterprise.so")
		if err != nil {
			log.Printf("LICENSE: error loading enterprise plugin: %v", err)
			log.Println("LICENSE: pulling compatible plugin from server (version: " + s.Version + ")")
			// Pull down the enterprise plugin since we have a valid license, perhaps we had a old version
			if !licenseCheckBucket.Allow() {
				log.Println("LICENSE: license check bucket is full, skipping plugin download")
				return
			}
			err = DownloadFileUsingLicenseKey(fmt.Sprintf("ludus-enterprise_%s.so", s.VersionString), "ludus-enterprise.so", pluginsDir, s.Version, s.LicenseKey, LicenseProductLudus)
			if err != nil {
				log.Printf("LICENSE: error getting enterprise plugin: %v", err)
			}
		} else {
			enterpriseLoaded = true
		}
	} else {
		log.Println("LICENSE: no enterprise plugin found, pulling compatible plugin from server")
		if !licenseCheckBucket.Allow() {
			log.Println("LICENSE: license check bucket is full, skipping plugin download")
			return
		}
		err = DownloadFileUsingLicenseKey(fmt.Sprintf("ludus-enterprise_%s.so", s.VersionString), "ludus-enterprise.so", pluginsDir, s.Version, s.LicenseKey, LicenseProductLudus)
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

func GetSubscriptionRolesMetadata(e *core.RequestEvent) ([]dto.GetSubscriptionRolesResponseItem, error) {

	// Check the request cache first
	subscriptionRolesFromCache := e.Get("rolesJSON")
	if subscriptionRolesFromCache != nil {
		return subscriptionRolesFromCache.([]dto.GetSubscriptionRolesResponseItem), nil
	}

	// First get the version of the role from latest
	err := DownloadFileUsingLicenseKey("roles.json", "roles.json", "/tmp", server.Version, server.LicenseKey, licenseProductSubscriptionRolesMetadata)
	if err != nil {
		return nil, err
	}
	rolesJSON, err := os.ReadFile("/tmp/roles.json")
	if err != nil {
		return nil, err
	}
	// Read the json into an array of GetSubscriptionRolesResponseItem
	var subscriptionRoles []dto.GetSubscriptionRolesResponseItem
	err = json.Unmarshal(rolesJSON, &subscriptionRoles)
	if err != nil {
		return nil, err
	}
	e.Set("rolesJSON", subscriptionRoles)
	return subscriptionRoles, nil
}

func DownloadRoleUsingLicenseKey(e *core.RequestEvent, roleName string, targetDir string) (string, error) {

	// Get the subscription roles metadata
	subscriptionRoles, err := GetSubscriptionRolesMetadata(e)
	if err != nil {
		return "", err
	}

	// Find the version of the role
	var roleVersion string
	var rolePackageUUID string
	for _, role := range subscriptionRoles {
		if role.Role == roleName {
			roleVersion = role.Version
			rolePackageUUID = role.PackageUUID
			break
		}
	}
	if roleVersion == "" || rolePackageUUID == "" {
		return "", errors.New("role " + roleName + " not found")
	}

	roleFileName := fmt.Sprintf("%s_v%s.tar.gz", roleName, roleVersion)
	err = DownloadFileUsingLicenseKey(roleFileName, roleFileName, targetDir, server.Version, server.LicenseKey, rolePackageUUID)
	if err != nil {
		return "", err
	}
	return roleFileName, nil
}

func DownloadFileUsingLicenseKey(path string, fileName string, targetDir string, version string, licenseKey string, packageUUID string) error {

	// If the file path doesn't start with /artifacts/, add it
	if !strings.HasPrefix(path, "artifacts/") || !strings.HasPrefix(path, "/artifacts/") {
		path = "artifacts/" + path
	}

	// Check for a .local-testing file in the target directory
	if _, err := os.Stat(targetDir + "/.local-testing"); err == nil {
		log.Printf("LICENSE: In local-testing mode (%s/.local-testing exists), skipping file download\n", targetDir)
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
	keygen.Package = packageUUID
	ctx := context.Background()

	if os.Getenv("LUDUS_DEBUG_LICENSE") == "1" {
		keygen.Logger = keygen.NewLogger(keygen.LogLevelDebug)
	}

	artifact := &keygen.Artifact{}
	response, err := client.Get(ctx, path, nil, artifact)
	if err != nil {
		logger.Error(fmt.Sprintf("LICENSE: unable to download file %s: %v", fileName, err))
		return err
	}
	artifact.URL = response.Headers.Get("Location")
	// Write the binary to disk
	if !FileExists(targetDir) {
		err := os.MkdirAll(targetDir, 0755)
		if err != nil {
			logger.Error(fmt.Sprintf("LICENSE: unable to create target directory: %v", err))
			return err
		}
	}
	targetPath := filepath.Join(targetDir, fileName)
	targetFile, err := os.Create(targetPath)
	if err != nil {
		logger.Error(fmt.Sprintf("LICENSE: unable to create target file %s: %v", fileName, err))
		return err
	}
	defer targetFile.Close()

	// Download the actual binary
	targetResp, err := http.Get(artifact.URL)
	if err != nil {
		logger.Error(fmt.Sprintf("LICENSE: unable to download target file %s: %v", fileName, err))
		return err
	}
	defer targetResp.Body.Close()

	// Copy the binary to the file
	_, err = io.Copy(targetFile, targetResp.Body)
	if err != nil {
		logger.Error(fmt.Sprintf("LICENSE: unable to write %s target file: %v", fileName, err))
		return err
	}

	// Verify the signature
	if err := VerifySignature(targetPath, artifact.Signature, binaryPublicKey, packageUUID); err != nil {
		logger.Error(fmt.Sprintf("LICENSE: unable to verify signature for %s target file: %v", fileName, err))
		return err
	}
	logger.Debug(fmt.Sprintf("LICENSE: successfully verified signature for %s target file", fileName))

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
		// Fall back to the Ludus product UUID
		if strings.Contains(err.Error(), "invalid signature") {
			opts := &ed25519.Options{
				Hash:    crypto.SHA512,
				Context: LicenseProductLudus,
			}
			err = ed25519.VerifyWithOptions(publicKey, checksum, signature, opts)
			if err != nil {
				return fmt.Errorf("failed to verify ed25519ph signature with package and product UUID: %v", err)
			} else {
				return nil
			}
		}
		return fmt.Errorf("failed to verify ed25519ph signature: %v", err)
	}

	return nil
}
