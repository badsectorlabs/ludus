package ludusapi

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Create struct for reading SQLite users (without CreatedAt/UpdatedAt)
type SQLiteUserObject struct {
	Name                  string    `json:"name"`
	UserID                string    `json:"userID"`
	DateCreated           time.Time `json:"dateCreated"`
	DateLastActive        time.Time `json:"dateLastActive"`
	IsAdmin               bool      `json:"isAdmin"`
	HashedAPIKey          string    `json:"-"`
	ProxmoxUsername       string    `json:"proxmoxUsername"`
	PortforwardingEnabled bool      `json:"portforwardingEnabled"`
}

// Create temporary struct for reading SQLite ranges (with string fields for arrays)
type SQLiteRangeObject struct {
	UserID         string    `json:"userID"`
	RangeNumber    int32     `json:"rangeNumber"`
	LastDeployment time.Time `json:"lastDeployment"`
	NumberOfVMs    int32     `json:"numberOfVMs"`
	TestingEnabled bool      `json:"testingEnabled"`
	AllowedDomains string    `json:"allowedDomains"` // SQLite stores as string
	AllowedIPs     string    `json:"allowedIPs"`     // SQLite stores as string
	RangeState     string    `json:"rangeState"`
}

// MigrateFromSQLiteToPocketBase migrates data from SQLite to PocketBase if conditions are met
func MigrateFromSQLiteToPocketBase() error {

	// Check if the migration has already been run
	if FileExists(fmt.Sprintf("%s/install/.sqlite_db_migrated", ludusInstallPath)) {
		logger.Debug(fmt.Sprintf("Migration has already been run (%s/install/.sqlite_db_migrated exists), skipping migration", ludusInstallPath))
		return nil
	}

	sqlitePath := fmt.Sprintf("%s/ludus.db", ludusInstallPath)

	// Check if SQLite database exists
	if _, err := os.Stat(sqlitePath); os.IsNotExist(err) {
		logger.Debug("SQLite database not found, skipping migration")
		return nil
	}

	// Check if PocketBase only has ROOT user
	var userCount int64
	userCount, err := app.CountRecords("users")
	if err != nil {
		return fmt.Errorf("error checking user count: %v", err)
	}
	if userCount > 1 {
		logger.Debug("New database has more than one user, skipping migration")
		return nil
	}

	logger.Info("Starting migration from SQLite to PocketBase...")

	// Open SQLite database
	sqliteDB, err := gorm.Open(sqlite.Open(sqlitePath), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("error opening SQLite database: %v", err)
	}

	// Begin transaction for migration
	err = app.RunInTransaction(func(txApp core.App) error {
		// Migrate ranges BEFORE users so the range records exist when we set defaultRangeID
		err := migrateRangesToPocketBase(txApp, sqliteDB)
		if err != nil {
			return err
		}
		err = migrateUsersToPocketBase(txApp, sqliteDB)
		if err != nil {
			return err
		}
		err = migrateVMsToPocketBase(txApp, sqliteDB)
		if err != nil {
			return err
		}
		err = migrateAccessesToPocketBase(txApp, sqliteDB)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error running transaction: %v", err)
	}

	// Migrate range files
	migrateRangeFiles()

	logger.Info("Migration from SQLite to PocketBase completed successfully")

	// Optionally, backup the SQLite database
	// backupPath := fmt.Sprintf("%s/ludus.db.backup.%s", ludusInstallPath, time.Now().Format("20060102-150405"))
	// if err := os.Rename(sqlitePath, backupPath); err != nil {
	// 	log.Printf("Warning: Could not backup SQLite database: %v", err)
	// } else {
	// 	log.Printf("SQLite database backed up to: %s", backupPath)
	// }

	// Create the .sqlite_db_migrated file to prevent the migration from running again
	err = os.WriteFile(fmt.Sprintf("%s/install/.sqlite_db_migrated", ludusInstallPath), []byte{}, 0644)
	if err != nil {
		logger.Error(fmt.Sprintf("Error creating .sqlite_db_migrated file: %v", err))
	}

	return nil
}

func migrateRangeFiles() {
	// Read all the range config files from /opt/ludus/users/{username}/range-config.yml and create them at
	// /opt/ludus/ranges/{rangeID}/range-config.yml

	// First loop over all the user directories in /opt/ludus/users/
	userDirs, err := os.ReadDir(fmt.Sprintf("%s/users/", ludusInstallPath))
	if err != nil {
		logger.Error(fmt.Sprintf("Error reading user directories: %v", err))
	}

	for _, userDir := range userDirs {
		// Read the range config file from /opt/ludus/users/{username}/range-config.yml
		rangeConfig, err := os.ReadFile(fmt.Sprintf("%s/users/%s/range-config.yml", ludusInstallPath, userDir.Name()))
		if err != nil {
			logger.Error(fmt.Sprintf("Error reading range config file for user %s: %v", userDir.Name(), err))
			continue
		}

		// Look up the user ID using the ProxmoxUsername
		userRecord, err := app.FindFirstRecordByData("users", "proxmoxUsername", userDir.Name())
		if err != nil && err != sql.ErrNoRows {
			logger.Error(fmt.Sprintf("Error looking up user %s in database: %v", userDir.Name(), err))
			continue
		}
		if userRecord == nil {
			logger.Debug(fmt.Sprintf("User %s not found in PocketBase, skipping range config file", userDir.Name()))
			continue
		}

		// Create the range directory if it doesn't exist
		err = os.MkdirAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, userRecord.Get("userID")), 0755)
		if err != nil {
			logger.Error(fmt.Sprintf("Error creating range directory for user %s: %v", userRecord.Get("userID"), err))
			continue
		}

		// Create the range config file at /opt/ludus/ranges/{rangeID}/range-config.yml
		err = os.WriteFile(fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, userRecord.Get("userID")), rangeConfig, 0644)
		if err != nil {
			logger.Error(fmt.Sprintf("Error creating range config file for user %s: %v", userRecord.Get("userID"), err))
			continue
		}
		log.Printf("Migrated range config file for user %s", userRecord.Get("userID"))
	}

	// Chown the range directories to the ludus user
	chownDirToUsernameRecursive(fmt.Sprintf("%s/ranges/", ludusInstallPath), "ludus")
}

func migrateUsersToPocketBase(txApp core.App, sqliteDB *gorm.DB) error {

	// Migrate users
	var sqliteUsers []SQLiteUserObject
	if err := sqliteDB.Table("user_objects").Find(&sqliteUsers).Error; err != nil {
		return fmt.Errorf("error reading users from SQLite: %v", err)
	}

	collection, err := txApp.FindCollectionByNameOrId("users")
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to find collection: %v", err))
	}

	for _, sqliteUser := range sqliteUsers {
		if sqliteUser.UserID == "ROOT" {
			err = createRootUserAsSuperuserInPocketBase(txApp)
			if err != nil {
				return fmt.Errorf("error creating root user as superuser in PocketBase: %v", err)
			}
			continue
		}

		// Look up the range that has the user_id of the user
		var rangeObj SQLiteRangeObject
		if err := sqliteDB.Table("range_objects").Where("user_id = ?", sqliteUser.UserID).First(&rangeObj).Error; err != nil {
			logger.Error(fmt.Sprintf("Error looking up range for user %s: %v", sqliteUser.UserID, err))
			continue
		}

		// Check if user already exists in PocketBase
		existingUser, err := txApp.FindRecordById("users", sqliteUser.UserID)
		if err == nil {
			// if the user doesn't have a user number, set it to the range number
			if existingUser.GetInt("userNumber") == 0 {
				existingUser.Set("userNumber", rangeObj.RangeNumber)
			}

			logger.Info(fmt.Sprintf("User %s already exists in PocketBase, skipping", sqliteUser.UserID))
			continue
		}

		// Read the password from the user's proxmox_password file
		passwordBytes, err := os.ReadFile(fmt.Sprintf("%s/users/%s/proxmox_password", ludusInstallPath, sqliteUser.ProxmoxUsername))
		if err != nil {
			logger.Error(fmt.Sprintf("Error reading proxmox password for user %s: %v", sqliteUser.ProxmoxUsername, err))
			continue
		}
		password := strings.Trim(string(passwordBytes), "\n")
		// Lookup the user in the database
		userRecord, err := txApp.FindFirstRecordByData("users", "userID", sqliteUser.UserID)
		if err != nil && err != sql.ErrNoRows {
			logger.Error(fmt.Sprintf("Error looking up user %s in database: %v", sqliteUser.UserID, err))
			continue
		}

		if userRecord == nil {
			logger.Debug(fmt.Sprintf("User %s not found in PocketBase, creating", sqliteUser.UserID))
			userRecord = core.NewRecord(collection)
		}

		if userRecord.Get("proxmoxTokenID") == "" {
			logger.Debug(fmt.Sprintf("Creating proxmox API token for existing PocketBase user %s", sqliteUser.ProxmoxUsername))
			tokenID, tokenSecret, err := createProxmoxAPITokenForUserWithoutContext(sqliteUser.ProxmoxUsername, "pam", password)
			if err != nil {
				logger.Error(fmt.Sprintf("Error creating proxmox API token for user %s: %v", sqliteUser.ProxmoxUsername, err))
				continue
			}
			userRecord.Set("proxmoxTokenID", tokenID)
			encryptedTokenSecret, err := EncryptStringForDatabase(tokenSecret)
			if err != nil {
				logger.Error(fmt.Sprintf("Error encrypting proxmox token secret for user %s: %v", sqliteUser.ProxmoxUsername, err))
				continue
			}
			userRecord.Set("proxmoxTokenSecret", encryptedTokenSecret)
		} else {
			logger.Debug(fmt.Sprintf("User %s has a proxmox API token: %s with secret: %s", sqliteUser.UserID, userRecord.Get("proxmoxTokenID"), userRecord.Get("proxmoxTokenSecret")))
		}

		logger.Debug(fmt.Sprintf("Creating user %s in PocketBase", sqliteUser.ProxmoxUsername))
		userRecord.SetEmail(sqliteUser.ProxmoxUsername + "@ludus.internal") // .internal is a reserved TLD for internal use, see: https://www.icann.org/en/board-activities-and-meetings/materials/approved-resolutions-special-meeting-of-the-icann-board-29-07-2024-en#section2.a
		userRecord.SetPassword(password)
		userRecord.Set("name", sqliteUser.Name)
		userRecord.Set("userID", sqliteUser.UserID)
		userRecord.Set("userNumber", rangeObj.RangeNumber)
		userRecord.Set("isAdmin", sqliteUser.IsAdmin)
		userRecord.Set("proxmoxUsername", sqliteUser.ProxmoxUsername)
		encryptedPassword, err := EncryptStringForDatabase(password)
		if err != nil {
			logger.Error(fmt.Sprintf("Error encrypting proxmox password for user %s: %v", sqliteUser.ProxmoxUsername, err))
			continue
		}
		userRecord.Set("proxmoxPassword", encryptedPassword)
		userRecord.Set("proxmoxRealm", "pam") // Ludus 1.x only supported PAM authentication
		userRecord.Set("hashedAPIKey", sqliteUser.HashedAPIKey)
		userRecord.Set("lastActive", sqliteUser.DateLastActive)
		userRecord.Set("defaultRangeID", rangeObj.UserID)

		// Look up the user's default range record so we can add it to their ranges relationship
		defaultRangeRecord, err := txApp.FindFirstRecordByData("ranges", "rangeID", rangeObj.UserID)
		if err != nil {
			logger.Error(fmt.Sprintf("Error looking up default range for user %s: %v", sqliteUser.UserID, err))
		} else {
			// Add the default range to the user's ranges relationship (using the PocketBase record ID)
			userRecord.Set("ranges", []string{defaultRangeRecord.Id})
		}

		if err := txApp.Save(userRecord); err != nil {
			logger.Error(fmt.Sprintf("Failed to create user: %v", err))
		}

		logger.Info(fmt.Sprintf("Successfully created PocketBase user with ID: %s", userRecord.Id))

		logger.Info(fmt.Sprintf("Migrated user: %s", sqliteUser.UserID))
	}
	return nil
}

func migrateRangesToPocketBase(txApp core.App, sqliteDB *gorm.DB) error {

	// Migrate ranges (excluding ROOT's range which already exists)
	var sqliteRanges []SQLiteRangeObject
	if err := sqliteDB.Table("range_objects").Find(&sqliteRanges).Error; err != nil {
		return fmt.Errorf("error reading ranges from SQLite: %v", err)
	}

	for _, sqliteRange := range sqliteRanges {
		if sqliteRange.UserID == "ROOT" {
			// Check if the user already has an API key
			rootRecord, err := txApp.FindFirstRecordByData("users", "userID", "ROOT")
			if err == nil && rootRecord.Get("proxmoxTokenID") != "" {
				logger.Info("User ROOT already has an API key, skipping migration")
				continue
			}
			// Only create an API key for ROOT, otherwise skip migration
			tokenID, tokenSecret, err := createRootAPITokenWithShell()
			if err != nil {
				// This is a fatal error, as range creation action uses the root proxmox API token
				log.Fatalf("Error creating proxmox API token for user root@pam: %v", err)
			}

			if rootRecord == nil {
				usersCollection, err := txApp.FindCollectionByNameOrId("users")
				if err != nil {
					return fmt.Errorf("error finding admin collection: %v", err)
				}
				rootRecord = core.NewRecord(usersCollection)
				rootRecord.Set("userID", "ROOT")
				rootRecord.Set("name", "root")
				rootRecord.Set("email", "root@ludus.internal")
				rootRecord.Set("password", security.RandomString(25)) // Will never be used, but needed to create the record
				rootRecord.Set("proxmoxUsername", "root")
				rootRecord.Set("proxmoxRealm", "pam")
				rootRecord.Set("userNumber", 1)
				rootRecord.Set("isAdmin", true)
				rootAPIKey, err := os.ReadFile(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath))
				if err != nil {
					logger.Error(fmt.Sprintf("Error reading root API key: %v", err))
					continue
				}
				rootAPIKeyString := strings.Trim(string(rootAPIKey), "\n")
				hashedAPIKey, err := HashString(rootAPIKeyString)
				if err != nil {
					logger.Error(fmt.Sprintf("Error hashing root API key: %v", err))
					continue
				}
				rootRecord.Set("hashedAPIKey", hashedAPIKey)
			}

			rootRecord.Set("proxmoxTokenID", tokenID)
			encryptedSecret, err := EncryptStringForDatabase(tokenSecret)
			if err != nil {
				return fmt.Errorf("error encrypting proxmox API token for user root@pam: %v", err)
			}
			rootRecord.Set("proxmoxTokenSecret", encryptedSecret)
			if err := txApp.Save(rootRecord); err != nil {
				return fmt.Errorf("error saving root user object: %v", err)
			}
			continue
		}

		// Check if range already exists in PocketBase
		existingRange, err := txApp.FindFirstRecordByData("ranges", "rangeNumber", sqliteRange.RangeNumber)
		if existingRange != nil {
			logger.Info(fmt.Sprintf("Range %d already exists in PocketBase, skipping", sqliteRange.RangeNumber))
			continue
		}
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("error finding range %d: %v", sqliteRange.RangeNumber, err)
		}

		// Convert string arrays to []string
		var allowedDomains []string
		var allowedIPs []string

		if sqliteRange.AllowedDomains != "" {
			// Parse pipe-separated string into slice (SQLite uses "|" as separator)
			allowedDomains = strings.Split(sqliteRange.AllowedDomains, "|")
			// Trim whitespace from each element
			for i, domain := range allowedDomains {
				allowedDomains[i] = strings.TrimSpace(domain)
			}
		}

		if sqliteRange.AllowedIPs != "" {
			// Parse pipe-separated string into slice (SQLite uses "|" as separator)
			allowedIPs = strings.Split(sqliteRange.AllowedIPs, "|")
			// Trim whitespace from each element
			for i, ip := range allowedIPs {
				allowedIPs[i] = strings.TrimSpace(ip)
			}
		}

		// Set default values for new fields (not present in old SQLite schema)
		name := fmt.Sprintf("Default Range for %s", sqliteRange.UserID)
		description := "Range migrated from Ludus 1.x"
		purpose := "General testing and development"

		rangeCollection, err := txApp.FindCollectionByNameOrId("ranges")
		if err != nil {
			return fmt.Errorf("error finding ranges collection: %v", err)
		}
		rangeRecord := core.NewRecord(rangeCollection)
		rangeRecord.Set("rangeNumber", sqliteRange.RangeNumber)
		rangeRecord.Set("rangeID", sqliteRange.UserID)
		rangeRecord.Set("name", name)
		rangeRecord.Set("description", description)
		rangeRecord.Set("purpose", purpose)
		rangeRecord.Set("lastDeployment", sqliteRange.LastDeployment)
		rangeRecord.Set("numberOfVMs", sqliteRange.NumberOfVMs)
		rangeRecord.Set("testingEnabled", sqliteRange.TestingEnabled)
		rangeRecord.Set("allowedDomains", allowedDomains)
		rangeRecord.Set("allowedIPs", allowedIPs)
		rangeRecord.Set("rangeState", sqliteRange.RangeState)
		if err := txApp.Save(rangeRecord); err != nil {
			return fmt.Errorf("error saving range %d: %v", sqliteRange.RangeNumber, err)
		}

		// Grant the range owner access to their pool (pool name is the rangeID/UserID)
		// This ensures the PVEPoolUser role is added to existing pools during migration
		if poolExists(sqliteRange.UserID) {
			// Look up the user from SQLite to get their proxmox username
			var sqliteUser SQLiteUserObject
			if err := sqliteDB.Table("user_objects").Where("user_id = ?", sqliteRange.UserID).First(&sqliteUser).Error; err == nil {
				err = giveUserAccessToRange(sqliteUser.ProxmoxUsername, "pam", sqliteRange.UserID, int(sqliteRange.RangeNumber))
				if err != nil {
					logger.Error(fmt.Sprintf("Error granting pool access to user %s for pool %s: %v", sqliteUser.ProxmoxUsername, sqliteRange.UserID, err))
					// Don't fail the migration if pool access fails, but log it
				} else {
					logger.Info(fmt.Sprintf("Granted pool access to user %s for pool %s", sqliteUser.ProxmoxUsername, sqliteRange.UserID))
				}
				err = grantGroupAccessToRangeInProxmox("ludus_admins", sqliteRange.UserID, int(sqliteRange.RangeNumber))
				if err != nil {
					logger.Error(fmt.Sprintf("Error granting group access to user %s for pool %s: %v", sqliteUser.ProxmoxUsername, sqliteRange.UserID, err))
					// Don't fail the migration if group access fails, but log it
				} else {
					logger.Info(fmt.Sprintf("Granted ludus_admins group access to pool %s", sqliteRange.UserID))
				}
			} else {
				logger.Debug(fmt.Sprintf("Could not find user %s in SQLite for pool access grant, will be handled during user migration", sqliteRange.UserID))
			}
		} else {
			logger.Debug(fmt.Sprintf("Pool %s does not exist, skipping pool access grant", sqliteRange.UserID))
		}

		logger.Info(fmt.Sprintf("Migrated range: %d (User: %s)", sqliteRange.RangeNumber, sqliteRange.UserID))
	}
	return nil
}

func migrateVMsToPocketBase(txApp core.App, sqliteDB *gorm.DB) error {
	// Create temporary struct for reading SQLite VMs (without CreatedAt/UpdatedAt)
	type SQLiteVmObject struct {
		ID          uint   `json:"id"`
		ProxmoxID   int32  `json:"proxmoxID"`
		RangeNumber int32  `json:"rangeNumber"`
		Name        string `json:"name"`
		PoweredOn   bool   `json:"poweredOn"`
		IP          string `json:"ip,omitempty"`
	}

	// Migrate VMs
	var sqliteVMs []SQLiteVmObject
	if err := sqliteDB.Table("vm_objects").Find(&sqliteVMs).Error; err != nil {
		return fmt.Errorf("error reading VMs from SQLite: %v", err)
	}

	for _, sqliteVM := range sqliteVMs {
		// Check if VM already exists in PocketBase
		vmCheckRecord, err := txApp.FindFirstRecordByData("vms", "proxmoxID", sqliteVM.ProxmoxID)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("error finding VM %d: %v", sqliteVM.ProxmoxID, err)
		}
		if vmCheckRecord != nil {
			logger.Info(fmt.Sprintf("VM %d in range %d already exists in PocketBase, skipping", sqliteVM.ProxmoxID, sqliteVM.RangeNumber))
			continue
		}

		vmCollection, err := txApp.FindCollectionByNameOrId("vms")
		if err != nil {
			return fmt.Errorf("error finding vms collection: %v", err)
		}
		vmRecord := core.NewRecord(vmCollection)
		vmRecord.Set("proxmoxID", sqliteVM.ProxmoxID)
		vmRecord.Set("name", sqliteVM.Name)
		vmRecord.Set("poweredOn", sqliteVM.PoweredOn)
		vmRecord.Set("ip", sqliteVM.IP)
		// Lookup the range record
		rangeRecord, err := txApp.FindFirstRecordByData("ranges", "rangeNumber", sqliteVM.RangeNumber)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("error finding range %d: %v", sqliteVM.RangeNumber, err)
		}
		if rangeRecord == nil {
			logger.Error(fmt.Sprintf("Warning: Could not find range %d, skipping VM %d", sqliteVM.RangeNumber, sqliteVM.ProxmoxID))
			continue
		}
		vmRecord.Set("range", rangeRecord.Id)
		if err := txApp.Save(vmRecord); err != nil {
			return fmt.Errorf("error saving VM %d: %v", sqliteVM.ProxmoxID, err)
		}

		logger.Info(fmt.Sprintf("Migrated VM: %d (Range: %d)", sqliteVM.ProxmoxID, sqliteVM.RangeNumber))
	}
	return nil
}

func migrateAccessesToPocketBase(txApp core.App, sqliteDB *gorm.DB) error {
	// Create temporary struct for reading SQLite range access objects (with string field for array)
	type SQLiteRangeAccessObject struct {
		TargetUserID  string `json:"targetUserID"`
		SourceUserIDs string `json:"sourceUserIDs"` // SQLite stores as string
	}

	// Migrate RangeAccessObjects to UserRangeAccess entries
	var sqliteRangeAccesses []SQLiteRangeAccessObject
	if err := sqliteDB.Table("range_access_objects").Find(&sqliteRangeAccesses).Error; err != nil {
		return fmt.Errorf("error reading range access objects from SQLite: %v", err)
	}

	for _, rangeAccess := range sqliteRangeAccesses {
		// Convert string array to []string
		var sourceUserIDs []string
		if rangeAccess.SourceUserIDs != "" {
			// Parse pipe-separated string into slice (SQLite uses "|" as separator)
			sourceUserIDs = strings.Split(rangeAccess.SourceUserIDs, "|")
			// Trim whitespace from each element
			for i, userID := range sourceUserIDs {
				sourceUserIDs[i] = strings.TrimSpace(userID)
			}
		}

		// For each source user ID, create a UserRangeAccess entry
		for _, sourceUserID := range sourceUserIDs {
			// Find the range number for the target user
			targetRangeRecord, err := txApp.FindFirstRecordByData("ranges", "rangeID", rangeAccess.TargetUserID)
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("error finding range for target user %s: %v", rangeAccess.TargetUserID, err)
			}
			if targetRangeRecord == nil {
				logger.Error(fmt.Sprintf("Warning: Could not find range for target user %s, skipping access grant", rangeAccess.TargetUserID))
				continue
			}

			// Check if this access already exists
			userRecord, err := txApp.FindFirstRecordByData("users", "userID", sourceUserID)
			if err != nil && err != sql.ErrNoRows {
				return fmt.Errorf("error finding user %s: %v", sourceUserID, err)
			}
			if userRecord == nil {
				logger.Error(fmt.Sprintf("Warning: Could not find user %s, skipping access grant", sourceUserID))
				continue
			}

			// Create UserRangeAccess entry
			userRecord.Set("ranges+", targetRangeRecord.Id)
			if err := txApp.Save(userRecord); err != nil {
				return fmt.Errorf("error saving user %s: %v", sourceUserID, err)
			}
			logger.Info(fmt.Sprintf("Migrated access: User %s -> Range %d (Target User: %s)", sourceUserID, targetRangeRecord.GetInt("rangeNumber"), rangeAccess.TargetUserID))
		}
	}
	return nil
}
