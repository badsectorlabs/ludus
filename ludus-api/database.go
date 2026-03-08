package ludusapi

import (
	"bytes"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
	"gopkg.in/yaml.v2"

	"ludusapi/dto"
	_ "ludusapi/migrations"
	"ludusapi/models"
)

// initialAdminYAML matches the structure written by the install form (ludus-server/form.go).
type initialAdminYAML struct {
	Name     string `yaml:"name"`
	Email    string `yaml:"email"`
	UserID   string `yaml:"userID"`
	Password string `yaml:"password"`
}

var reservedInitialAdminUserIDs = []string{"ADMIN", "ROOT", "CICD", "SHARED", "0"}

func InitDb() {
	if os.Geteuid() == 0 {
		// If a root-api-key file doesn't exist, recreate the root user in the database
		if !FileExists(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath)) {
			logger.Info("Creating root user in database")
			createRootUserInDatabase()
		}
		// If initial-admin.yml exists (from interactive install), create the initial admin user
		initialAdminPath := fmt.Sprintf("%s/install/initial-admin.yml", ludusInstallPath)
		if FileExists(initialAdminPath) {
			logger.Info("Creating initial admin user from install config")
			if err := createInitialAdminFromFile(initialAdminPath); err != nil {
				logger.Error(fmt.Sprintf("Failed to create initial admin user: %v", err))
				os.Exit(2)
			}
		}
		// Check if there was a previous sqlite db, and if so, run the migrations
		if FileExists(fmt.Sprintf("%s/ludus.db", ludusInstallPath)) && !FileExists(fmt.Sprintf("%s/install/.sqlite_db_migrated", ludusInstallPath)) {
			logger.Info("SQLite database found, running migrations")
			if err := MigrateFromSQLiteToPocketBase(); err != nil {
				logger.Error(fmt.Sprintf("SQLite migration failed: %v", err))
				os.Exit(2)
			}
		}
	}
}

func createRootUserInDatabase() {

	// Check if the root user already exists in the database
	rootUserRecord, err := app.FindFirstRecordByData("users", "userID", "ROOT")
	if err != nil && err != sql.ErrNoRows {
		logger.Error(fmt.Sprintf("Error finding root user in database: %v", err))
		os.Exit(2)
	}
	if rootUserRecord != nil {
		logger.Info("Root user already exists in database, removing it")
		err = app.Delete(rootUserRecord)
		if err != nil {
			logger.Error(fmt.Sprintf("Error deleting root user in database: %v", err))
			os.Exit(2)
		}
	}

	// Create a root user
	userCollection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding users collection: %v", err))
		os.Exit(2)
	}
	userRecord := core.NewRecord(userCollection)
	user := &models.User{}
	user.SetProxyRecord(userRecord)
	user.SetName("root")
	user.SetProxmoxUsername("root")
	user.SetProxmoxRealm("pam")
	user.SetUserId("ROOT")
	user.SetUserNumber(1)
	user.SetIsAdmin(true)
	user.SetEmail("root@ludus.internal")

	rootWebPassword := security.RandomString(25)
	err = os.WriteFile(fmt.Sprintf("%s/install/root-web-password", ludusInstallPath), []byte(rootWebPassword), 0400)
	if err != nil {
		log.Fatal(err.Error())
	}
	os.Chown(fmt.Sprintf("%s/install/root-web-password", ludusInstallPath), 0, 0)
	user.SetPassword(rootWebPassword)

	apiKey := GenerateAPIKey(user.UserId())
	err = os.WriteFile(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath), []byte(apiKey), 0400)
	if err != nil {
		log.Fatal(err.Error())
	}
	os.Chown(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath), 0, 0)
	tokenID, tokenSecret, err := createRootAPITokenWithShell()
	if err != nil {
		log.Fatal(err.Error())
	}
	hashedAPIKey, err := HashString(apiKey)
	if err != nil {
		logger.Error(fmt.Sprintf("Error hashing root API key: %v", err))
		os.Exit(2)
	}
	user.SetHashedApikey(hashedAPIKey)

	encryptedTokenSecret, err := EncryptStringForDatabase(tokenSecret)
	if err != nil {
		log.Fatal("error encrypting Root API Token secret")
	}

	user.SetProxmoxTokenId(tokenID)
	user.SetProxmoxTokenSecret(encryptedTokenSecret)

	os.MkdirAll(fmt.Sprintf("%s/users/root", ludusInstallPath), 0700)

	err = app.Save(userRecord)
	if err != nil {
		logger.Error(fmt.Sprintf("Error saving root user in database: %v", err))
		os.Exit(2)
	}

	// Also create the root user as a superuser in PocketBase
	err = createRootUserAsSuperuserInPocketBase(app)
	if err != nil {
		logger.Error(fmt.Sprintf("Error creating root user as superuser in PocketBase: %v", err))
		os.Exit(2)
	}

	logger.Info("Successfully created root user in database")
}

func createInitialAdminFromFile(initialAdminPath string) error {
	data, err := os.ReadFile(initialAdminPath)
	if err != nil {
		return fmt.Errorf("reading initial-admin.yml: %w", err)
	}
	var cfg initialAdminYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parsing initial-admin.yml: %w", err)
	}
	if cfg.Name == "" || cfg.Email == "" || cfg.UserID == "" || cfg.Password == "" {
		return fmt.Errorf("initial-admin.yml must contain name, email, userID, and password")
	}
	if !UserIDRegex.MatchString(cfg.UserID) {
		return fmt.Errorf("initial admin userID does not match ^[A-Za-z0-9]{1,20}$")
	}
	if slices.Contains(reservedInitialAdminUserIDs, strings.ToUpper(cfg.UserID)) {
		return fmt.Errorf("%s is a reserved user ID", cfg.UserID)
	}
	if len(cfg.Password) < 8 {
		return fmt.Errorf("initial admin password must be at least 8 characters")
	}

	proxmoxUsername := strings.ReplaceAll(strings.ToLower(cfg.Name), " ", "-")

	matchingUsers, err := app.CountRecords("users", dbx.HashExp{"userID": cfg.UserID})
	if err != nil {
		return fmt.Errorf("checking if user exists: %w", err)
	}
	if matchingUsers > 0 {
		return fmt.Errorf("user with ID %s already exists", cfg.UserID)
	}
	matchingUsers, err = app.CountRecords("users", dbx.HashExp{"proxmoxUsername": proxmoxUsername})
	if err != nil {
		return fmt.Errorf("checking if username exists: %w", err)
	}
	if matchingUsers > 0 {
		return fmt.Errorf("user with name %s already exists", proxmoxUsername)
	}
	if userExistsOnHostSystem(proxmoxUsername) {
		return fmt.Errorf("username %s already exists on the host system", proxmoxUsername)
	}
	if poolExists(cfg.UserID) {
		return fmt.Errorf("pool %s already exists", cfg.UserID)
	}

	userCollection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return fmt.Errorf("finding users collection: %w", err)
	}
	userRecord := core.NewRecord(userCollection)
	user := &models.User{}
	user.SetProxyRecord(userRecord)
	user.SetName(cfg.Name)
	user.SetUserId(cfg.UserID)
	user.SetEmail(cfg.Email)
	user.SetPassword(cfg.Password)
	user.SetIsAdmin(true)
	user.SetProxmoxUsername(proxmoxUsername)
	user.SetProxmoxRealm("pam")
	encryptedPassword, err := EncryptStringForDatabase(cfg.Password)
	if err != nil {
		return fmt.Errorf("encrypting password: %w", err)
	}
	user.SetProxmoxPassword(encryptedPassword)

	wasError := false
	err = app.RunInTransaction(func(txApp core.App) error {
		defer func() {
			if wasError {
				removeUserFromHostSystem(user.ProxmoxUsername())
				removeUserFromProxmox(user.ProxmoxUsername(), "pam")
				removePool(user.UserId())
				defaultRangeRecord, findErr := txApp.FindFirstRecordByData("ranges", "rangeID", user.DefaultRangeId())
				if findErr == nil && defaultRangeRecord != nil {
					txApp.Delete(defaultRangeRecord)
				}
				txApp.Delete(user)
			}
		}()

		user.SetUserNumber(findNextAvailableUserNumber(txApp))
		if user.UserNumber() > 150 {
			wasError = true
			return fmt.Errorf("cannot create more than 150 users")
		}

		if err := CreateDefaultUserRangeForBootstrap(txApp, user); err != nil {
			wasError = true
			return fmt.Errorf("creating default range: %w", err)
		}

		extraVars := map[string]interface{}{
			"username":          user.ProxmoxUsername(),
			"user_id":           user.UserId(),
			"user_number":       user.UserNumber(),
			"proxmox_public_ip": ServerConfiguration.ProxmoxPublicIP,
			"user_is_admin":     true,
			"proxmox_password":  cfg.Password,
		}
		output, err := RunAddUserPlaybookStandalone(extraVars)
		if err != nil {
			wasError = true
			return fmt.Errorf("running add-user playbook: %w (output: %s)", err, output)
		}

		apiKey := GenerateAPIKey(user.UserId())
		hashedAPIKey, err := HashString(apiKey)
		if err != nil {
			wasError = true
			return fmt.Errorf("hashing API key: %w", err)
		}
		user.SetHashedApikey(hashedAPIKey)

		tokenID, tokenSecret, err := createProxmoxAPITokenForUserWithoutContext(user.ProxmoxUsername(), user.ProxmoxRealm(), cfg.Password)
		if err != nil {
			wasError = true
			return fmt.Errorf("creating Proxmox API token: %w", err)
		}
		encryptedTokenSecret, err := EncryptStringForDatabase(tokenSecret)
		if err != nil {
			wasError = true
			return fmt.Errorf("encrypting Proxmox token: %w", err)
		}
		user.SetProxmoxTokenId(tokenID)
		user.SetProxmoxTokenSecret(encryptedTokenSecret)

		// Grant user and ludus_admins Proxmox pool/SDN access to the user's default range
		// (required for range deployment, especially SDN.Use in cluster mode)
		if err := GrantUserProxmoxAccessToDefaultRange(txApp, user); err != nil {
			wasError = true
			return fmt.Errorf("granting Proxmox access to default range: %w", err)
		}

		if err := txApp.Save(user); err != nil {
			wasError = true
			return fmt.Errorf("saving user: %w", err)
		}

		os.MkdirAll(fmt.Sprintf("%s/users/%s", ludusInstallPath, user.ProxmoxUsername()), 0700)

		credentialsPath := fmt.Sprintf("%s/install/initial-admin-credentials", ludusInstallPath)
		credentialsContent := fmt.Sprintf("email:%s\nusername:%s\napi_key:%s\npassword:%s\n", user.Email(), user.ProxmoxUsername(), apiKey, cfg.Password)
		if err := os.WriteFile(credentialsPath, []byte(credentialsContent), 0600); err != nil {
			logger.Error(fmt.Sprintf("Failed to write initial-admin-credentials: %v", err))
			// non-fatal: we still created the user
		} else {
			os.Chown(credentialsPath, 0, 0)
		}

		return nil
	})
	if err != nil {
		return err
	}
	if removeErr := os.Remove(initialAdminPath); removeErr != nil {
		logger.Error(fmt.Sprintf("Failed to remove initial-admin.yml: %v", removeErr))
	}
	logger.Info("Successfully created initial admin user")
	return nil
}

func createRootUserAsSuperuserInPocketBase(txApp core.App) error {
	logger.Info("Making root user a superuser in PocketBase - Password is the ROOT API key")
	adminCollection, err := txApp.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		logger.Error(fmt.Sprintf("'_superusers' collection not found: %v", err))
	}

	newAdmin := core.NewRecord(adminCollection)

	newAdmin.SetEmail("root@ludus.internal")

	// The password for the new superuser is the ROOT API key
	rootAPIKey, err := os.ReadFile(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath))
	if err != nil {
		logger.Error(fmt.Sprintf("Error reading root API key: %v", err))
	}
	rootAPIKeyString := strings.Trim(string(rootAPIKey), "\n")
	newAdmin.SetPassword(rootAPIKeyString)

	if err := txApp.Save(newAdmin); err != nil {
		logger.Error(fmt.Sprintf("failed to save new superuser: %v", err))
	}
	logger.Debug("Successfully made root@ludus.internal a superuser in PocketBase")
	return nil
}

// findNextAvailableRangeNumber finds the smallest positive integer that is not
// present in the RangeNumber column of the RangeObject table. This function
// assumes that the RangeNumber values are positive integers and that there can
// be gaps or non-sequential values in the column. It will also skip any values
// in the reserved_range_numbers array in the config.
//
// Returns:
//
//	int - The smallest positive integer that is not present in the
//	        RangeNumber column of the RangeObject table.
func findNextAvailableRangeNumber(txApp core.App) int {

	type RangeResult struct {
		RangeNumber int `db:"rangeNumber" json:"rangeNumber"`
	}

	var queryResult []RangeResult
	var rangeNumbers []int

	err := txApp.DB().
		Select("rangeNumber").
		From("ranges").
		OrderBy("rangeNumber").
		All(&queryResult)
	if err != nil {
		return -1
	}

	for _, item := range queryResult {
		rangeNumbers = append(rangeNumbers, item.RangeNumber)
	}

	for i := int(1); ; i++ {
		found := false
		for _, num := range rangeNumbers {
			if num == i {
				found = true
				break
			}
		}
		if !found {
			// Check if this is a reserved range number
			for _, num := range ServerConfiguration.ReservedRangeNumbers {
				if int(num) == i {
					found = true
					break
				}
			}
			// The number is not in the DB and not reserved, return it
			if !found {
				return i
			}
		}
	}
}

// findNextAvailableUserNumber finds the smallest positive integer that is not
// present in the UserNumber column of the UserObject table. This function
// assumes that the UserNumber values are positive integers and that there can
// be gaps or non-sequential values in the column.
//
// Returns:
//
//	int - The smallest positive integer that is not present in the
//	        UserNumber column of the UserObject table.
func findNextAvailableUserNumber(txApp core.App) int {
	type UserRow struct {
		UserNumber int `db:"userNumber"`
	}
	var rows []UserRow

	err := txApp.DB().
		Select("userNumber").
		From("users").
		OrderBy("userNumber").
		All(&rows)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding next available user number: %v", err))
		return -1
	}

	// Start at 2 since 1 is reserved for the root user (198.51.100.1 is reserved for the server)
	for i := int(2); ; i++ {
		found := false
		for _, num := range rows {
			if num.UserNumber == i {
				found = true
				break
			}
		}
		if !found {
			return i
		}
	}
}

func generateUserIDFromOAuth2Provider(e *core.RecordAuthWithOAuth2RequestEvent) string {
	// Prefer Name, then Username from OAuth2 response
	source := strings.TrimSpace(e.OAuth2User.Name)
	if source == "" {
		source = strings.TrimSpace(e.OAuth2User.Username)
	}

	var base string
	if source != "" {
		words := strings.Fields(source)
		if len(words) >= 2 {
			// First initial + last initial, capitalized
			first := string(unicode.ToUpper(rune(words[0][0])))
			last := string(unicode.ToUpper(rune(words[len(words)-1][0])))
			base = first + last
		} else if len(words) == 1 {
			// Single name: first two letters uppercase
			word := words[0]
			if len(word) >= 2 {
				base = strings.ToUpper(word[:2])
			} else {
				base = strings.ToUpper(word) + strings.ToUpper(security.RandomString(2))
			}
		}
	}
	if base == "" {
		// No username or name: random 3 uppercase characters
		base = strings.ToUpper(security.RandomString(3))
	}

	userIDInUse := func(candidate string) bool {
		existing, err := e.App.FindFirstRecordByData("users", "userID", candidate)
		if err != nil && err != sql.ErrNoRows {
			return true // treat errors as in-use to avoid overwriting
		}
		return existing != nil
	}

	candidate := base
	for n := 2; ; n++ {
		if !userIDInUse(candidate) && !slices.Contains(reservedInitialAdminUserIDs, candidate) {
			return candidate
		}
		candidate = base + strconv.Itoa(n)
	}
}

func populateUserFieldsFromOAuth2Provider(e *core.RecordAuthWithOAuth2RequestEvent) error {
	if e.Record != nil {
		return e.Next()
	}

	name := strings.TrimSpace(e.OAuth2User.Name)
	if name == "" {
		name = strings.TrimSpace(e.OAuth2User.Username)
	}
	if name == "" {
		name = "OAuth2 User"
	}

	email := e.OAuth2User.Email
	if email == "" {
		return fmt.Errorf("OAuth2 provider did not return an email address")
	}

	userID := generateUserIDFromOAuth2Provider(e)
	password := security.RandomString(15)
	proxmoxUsername := strings.ReplaceAll(strings.ToLower(name), " ", "-")

	matchingUsers, err := e.App.CountRecords("users", dbx.HashExp{"proxmoxUsername": proxmoxUsername})
	if err != nil {
		return fmt.Errorf("checking if proxmox username exists: %w", err)
	}
	if matchingUsers > 0 {
		return fmt.Errorf("user with proxmox username %s already exists", proxmoxUsername)
	}
	if userExistsOnHostSystem(proxmoxUsername) {
		return fmt.Errorf("username %s already exists on the host system", proxmoxUsername)
	}
	if poolExists(userID) {
		return fmt.Errorf("pool %s already exists", userID)
	}

	// Running as non-root: proxy the provisioning request to the root API
	isAdmin := false
	tokenPayload := fmt.Sprintf("%s|%s|%d", userID, strconv.FormatBool(isAdmin), time.Now().Unix())
	encryptedToken, err := EncryptStringForDatabase(tokenPayload)
	if err != nil {
		return fmt.Errorf("encrypting internal token: %w", err)
	}

	reqBody := dto.ProvisionOAuth2UserRequest{
		Name:            name,
		Email:           email,
		UserID:          userID,
		Password:        password,
		ProxmoxUsername: proxmoxUsername,
		IsAdmin:         isAdmin,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshalling provision request: %w", err)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	httpReq, err := http.NewRequest(http.MethodPost, "https://127.0.0.1:8081/api/v2/user/provision-oauth2", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("creating provision request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Internal-Token", encryptedToken)

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("calling admin API for provisioning: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading provision response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("admin API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var provResp dto.ProvisionOAuth2UserResponse
	if err := json.Unmarshal(respBody, &provResp); err != nil {
		return fmt.Errorf("parsing provision response: %w", err)
	}

	record, err := e.App.FindRecordById("users", provResp.RecordID)
	if err != nil {
		return fmt.Errorf("loading provisioned user record: %w", err)
	}
	e.Record = record

	logger.Debug(fmt.Sprintf("Successfully provisioned OAuth2 user %s (%s)", userID, proxmoxUsername))
	return e.Next()
}
