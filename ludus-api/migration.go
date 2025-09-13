package ludusapi

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/supabase-community/auth-go/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// MigrateFromSQLite migrates data from SQLite to PostgreSQL if conditions are met
func MigrateFromSQLite() error {
	sqlitePath := fmt.Sprintf("%s/ludus.db", ludusInstallPath)

	// Check if SQLite database exists
	if _, err := os.Stat(sqlitePath); os.IsNotExist(err) {
		logger.Debug("SQLite database not found, skipping migration")
		return nil
	}

	// Check if PostgreSQL database only has ROOT user
	var userCount int64
	if err := db.Model(&UserObject{}).Count(&userCount).Error; err != nil {
		return fmt.Errorf("error checking user count: %v", err)
	}

	if userCount > 1 {
		logger.Debug("PostgreSQL database has more than ROOT user, skipping migration")
		return nil
	}

	log.Println("Starting migration from SQLite to PostgreSQL...")

	// Open SQLite database
	sqliteDB, err := gorm.Open(sqlite.Open(sqlitePath), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("error opening SQLite database: %v", err)
	}

	// Begin transaction for migration
	tx := db.Begin()
	if tx.Error != nil {
		return fmt.Errorf("error starting transaction: %v", tx.Error)
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("Migration failed, rolling back: %v", r)
		}
	}()

	// Create temporary struct for reading SQLite users (without CreatedAt/UpdatedAt)
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

	// Migrate users (excluding ROOT which already exists)
	var sqliteUsers []SQLiteUserObject
	if err := sqliteDB.Table("user_objects").Find(&sqliteUsers).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("error reading users from SQLite: %v", err)
	}

	for _, sqliteUser := range sqliteUsers {
		if sqliteUser.UserID == "ROOT" {
			continue // Skip ROOT user as it already exists in PostgreSQL
		}

		// Look up the range that has the user_id of the user
		var rangeObj RangeObject
		if err := sqliteDB.Table("range_objects").Where("user_id = ?", sqliteUser.UserID).First(&rangeObj).Error; err != nil {
			log.Printf("Error looking up range for user %s: %v", sqliteUser.UserID, err)
			continue
		}

		// Check if user already exists in PostgreSQL
		var existingUser UserObject
		if err := tx.Where("user_id = ?", sqliteUser.UserID).First(&existingUser).Error; err == nil {

			// if the user doesn't have a user number, set it to the range number
			if existingUser.UserNumber == 0 {
				existingUser.UserNumber = rangeObj.RangeNumber
				tx.Save(&existingUser)
			}

			log.Printf("User %s already exists in PostgreSQL, skipping", sqliteUser.UserID)
			continue
		}

		// Create PostgreSQL user object
		user := UserObject{
			Name:            sqliteUser.Name,
			UserID:          sqliteUser.UserID,
			UserNumber:      rangeObj.RangeNumber, // The user only has one range, so their "range number" becomes their user number
			DateCreated:     sqliteUser.DateCreated,
			DateLastActive:  sqliteUser.DateLastActive,
			IsAdmin:         sqliteUser.IsAdmin,
			HashedAPIKey:    sqliteUser.HashedAPIKey,
			ProxmoxUsername: sqliteUser.ProxmoxUsername,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}

		// Create user in PostgreSQL
		if err := tx.Create(&user).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("error creating user %s: %v", user.UserID, err)
		}
		log.Printf("Migrated user: %s", user.UserID)
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

	// Migrate ranges (excluding ROOT's range which already exists)
	var sqliteRanges []SQLiteRangeObject
	if err := sqliteDB.Table("range_objects").Find(&sqliteRanges).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("error reading ranges from SQLite: %v", err)
	}

	for _, sqliteRange := range sqliteRanges {
		if sqliteRange.UserID == "ROOT" {
			// Check if the user already has an API key
			var rootUserObject UserObject
			db.First(&rootUserObject, "user_id = ?", "ROOT")
			if rootUserObject.ProxmoxTokenID != "" {
				log.Printf("User ROOT already has an API key, skipping migration")
				continue
			}
			// Only create an API key for ROOT, otherwise skip migration
			tokenID, tokenSecret, err := createRootAPITokenWithShell()
			if err != nil {
				// This is a fatal error, as range creation action uses the root proxmox API token
				log.Fatalf("Error creating proxmox API token for user root@pam: %v", err)
			}

			rootUserObject.ProxmoxTokenID = tokenID
			encryptedSecret, err := EncryptStringForDatabase(tokenSecret)
			if err != nil {
				log.Printf("Error encrypting proxmox API token for user root@pam: %v", err)
			}
			rootUserObject.ProxmoxTokenSecret = encryptedSecret
			db.Save(&rootUserObject)
			continue
		}

		// Check if range already exists in PostgreSQL
		var existingRange RangeObject
		if err := tx.Where("range_number = ?", sqliteRange.RangeNumber).First(&existingRange).Error; err == nil {
			log.Printf("Range %d already exists in PostgreSQL, skipping", sqliteRange.RangeNumber)
			continue
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
		description := "Range migrated from SQLite"
		purpose := "General testing and development"

		// Create PostgreSQL range object
		rangeObj := RangeObject{
			RangeID:        sqliteRange.UserID,
			RangeNumber:    sqliteRange.RangeNumber,
			Name:           name,
			Description:    description,
			Purpose:        purpose,
			LastDeployment: sqliteRange.LastDeployment,
			NumberOfVMs:    sqliteRange.NumberOfVMs,
			TestingEnabled: sqliteRange.TestingEnabled,
			AllowedDomains: allowedDomains,
			AllowedIPs:     allowedIPs,
			RangeState:     sqliteRange.RangeState,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}

		// Create range in PostgreSQL
		if err := tx.Create(&rangeObj).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("error creating range %d: %v", rangeObj.RangeNumber, err)
		}
		log.Printf("Migrated range: %d (User: %s)", rangeObj.RangeNumber, rangeObj.RangeID)

		// Create UserRangeAccess record for the range owner
		userRangeAccess := UserRangeAccess{
			UserID:      rangeObj.RangeID,
			RangeNumber: rangeObj.RangeNumber,
		}
		if err := tx.Create(&userRangeAccess).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("error creating user range access for range %d: %v", rangeObj.RangeNumber, err)
		}
	}

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
		tx.Rollback()
		return fmt.Errorf("error reading VMs from SQLite: %v", err)
	}

	for _, sqliteVM := range sqliteVMs {
		// Check if VM already exists in PostgreSQL
		var existingVM VmObject
		if err := tx.Where("proxmox_id = ? AND range_number = ?", sqliteVM.ProxmoxID, sqliteVM.RangeNumber).First(&existingVM).Error; err == nil {
			log.Printf("VM %d in range %d already exists in PostgreSQL, skipping", sqliteVM.ProxmoxID, sqliteVM.RangeNumber)
			continue
		}

		// Create PostgreSQL VM object
		vm := VmObject{
			ID:          sqliteVM.ID,
			ProxmoxID:   sqliteVM.ProxmoxID,
			RangeNumber: sqliteVM.RangeNumber,
			Name:        sqliteVM.Name,
			PoweredOn:   sqliteVM.PoweredOn,
			IP:          sqliteVM.IP,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		// Create VM in PostgreSQL
		if err := tx.Create(&vm).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("error creating VM %d: %v", vm.ProxmoxID, err)
		}
		log.Printf("Migrated VM: %d (Range: %d)", vm.ProxmoxID, vm.RangeNumber)
	}

	// Create temporary struct for reading SQLite range access objects (with string field for array)
	type SQLiteRangeAccessObject struct {
		TargetUserID  string `json:"targetUserID"`
		SourceUserIDs string `json:"sourceUserIDs"` // SQLite stores as string
	}

	// Migrate RangeAccessObjects to UserRangeAccess entries
	var sqliteRangeAccesses []SQLiteRangeAccessObject
	if err := sqliteDB.Table("range_access_objects").Find(&sqliteRangeAccesses).Error; err != nil {
		tx.Rollback()
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
			var targetRange RangeObject
			if err := tx.Where("range_id = ?", rangeAccess.TargetUserID).First(&targetRange).Error; err != nil {
				log.Printf("Warning: Could not find range for target user %s, skipping access grant", rangeAccess.TargetUserID)
				continue
			}

			// Check if this access already exists
			var existingAccess UserRangeAccess
			if err := tx.Where("user_id = ? AND range_number = ?", sourceUserID, targetRange.RangeNumber).First(&existingAccess).Error; err == nil {
				log.Printf("Access for user %s to range %d already exists, skipping", sourceUserID, targetRange.RangeNumber)
				continue
			}

			// Create UserRangeAccess entry
			userRangeAccess := UserRangeAccess{
				UserID:      sourceUserID,
				RangeNumber: targetRange.RangeNumber,
			}
			if err := tx.Create(&userRangeAccess).Error; err != nil {
				tx.Rollback()
				return fmt.Errorf("error creating user range access for user %s to range %d: %v", sourceUserID, targetRange.RangeNumber, err)
			}
			log.Printf("Migrated access: User %s -> Range %d (Target User: %s)", sourceUserID, targetRange.RangeNumber, rangeAccess.TargetUserID)
		}
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("error committing migration: %v", err)
	}

	// Migrate existing users to Supabase
	migrateExistingUsersToSupabase()

	// Migrate range files
	migrateRangeFiles()

	log.Println("Migration from SQLite to PostgreSQL completed successfully")

	// Optionally, backup the SQLite database
	// backupPath := fmt.Sprintf("%s/ludus.db.backup.%s", ludusInstallPath, time.Now().Format("20060102-150405"))
	// if err := os.Rename(sqlitePath, backupPath); err != nil {
	// 	log.Printf("Warning: Could not backup SQLite database: %v", err)
	// } else {
	// 	log.Printf("SQLite database backed up to: %s", backupPath)
	// }

	return nil
}

func migrateExistingUsersToSupabase() {
	// Read the users from /etc/pve/user.cfg
	userCfg, err := os.ReadFile("/etc/pve/user.cfg")
	if err != nil {
		log.Printf("Error reading /etc/pve/user.cfg: %v", err)
		return
	}

	// Parse the user.cfg file
	userCfgLines := strings.Split(string(userCfg), "\n")
	// Find lines that start with "user: "
	for _, line := range userCfgLines {
		if strings.HasPrefix(line, "user:") {
			// Extract the username from the line
			usernamePlusExtra := strings.TrimPrefix(line, "user:")
			username := strings.Split(usernamePlusExtra, ":")[0]

			// Only migrate local PAM users (Ludus 1.x only supported local PAM users)
			if strings.Contains(username, "@pam") {

				username = strings.Split(username, "@pam")[0]

				// Ignore the root user
				if username == "root" {
					continue
				}

				// Read the password from the user's proxmox_password file
				passwordBytes, err := os.ReadFile(fmt.Sprintf("%s/users/%s/proxmox_password", ludusInstallPath, username))
				if err != nil {
					log.Printf("Error reading proxmox password for user %s: %v", username, err)
					continue
				}
				password := strings.Trim(string(passwordBytes), "\n")
				// Lookup the user in the database
				var user UserObject
				if err := db.Where("proxmox_username = ?", username).First(&user).Error; err != nil {
					log.Printf("Error looking up user %s in database, user folder exists on disk but not in database: %v", username, err)
					continue
				}

				userWithEmailAndPassword := UserWithEmailAndPassword{
					UserObject: user,
					Password:   password,
					Email:      user.ProxmoxUsername + "@ludus.localhost",
				}

				var supabaseUser types.User
				if user.UUID == uuid.Nil {
					supabaseUser, err = createUserInSupabase(userWithEmailAndPassword, password)
					if err != nil {
						log.Printf("Error creating user %s in Supabase: %v", username, err)
						continue
					}
					user.UUID = supabaseUser.ID
					db.Save(&user)
				}

				if user.ProxmoxTokenID == "" {
					tokenID, tokenSecret, err := createProxmoxAPITokenForUserWithoutContext(user)
					if err != nil {
						// This is a fatal error, as every user needs a Proxmox API token to be able to deploy VMs
						log.Fatalf("Error creating proxmox API token for user %s: %v", username, err)
					}
					user.ProxmoxTokenID = tokenID
					encryptedSecret, err := EncryptStringForDatabase(tokenSecret)
					if err != nil {
						log.Fatalf("Error encrypting proxmox API token for user %s: %v", username, err)
					}
					user.ProxmoxTokenSecret = encryptedSecret
					db.Save(&user)
				}

				log.Printf("Migrated user %s to Supabase", username)
			}
		}
	}

}

func migrateRangeFiles() {
	// Read all the range config files from /opt/ludus/users/{username}/range-config.yml and create them at
	// /opt/ludus/ranges/{rangeID}/range-config.yml

	// First loop over all the user directories in /opt/ludus/users/
	userDirs, err := os.ReadDir(fmt.Sprintf("%s/users/", ludusInstallPath))
	if err != nil {
		log.Fatalf("Error reading user directories: %v", err)
	}

	for _, userDir := range userDirs {
		// Read the range config file from /opt/ludus/users/{username}/range-config.yml
		rangeConfig, err := os.ReadFile(fmt.Sprintf("%s/users/%s/range-config.yml", ludusInstallPath, userDir.Name()))
		if err != nil {
			log.Printf("Error reading range config file for user %s: %v", userDir.Name(), err)
			continue
		}

		// Look up the user ID using the ProxmoxUsername
		var user UserObject
		if err := db.Where("proxmox_username = ?", userDir.Name()).First(&user).Error; err != nil {
			log.Printf("Error looking up user ID for user %s: %v", userDir.Name(), err)
			continue
		}

		// Create the range directory if it doesn't exist
		err = os.MkdirAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, user.UserID), 0755)
		if err != nil {
			log.Printf("Error creating range directory for user %s: %v", user.UserID, err)
			continue
		}

		// Create the range config file at /opt/ludus/ranges/{rangeID}/range-config.yml
		err = os.WriteFile(fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, user.UserID), rangeConfig, 0644)
		if err != nil {
			log.Printf("Error creating range config file for user %s: %v", user.UserID, err)
			continue
		}
		log.Printf("Migrated range config file for user %s", user.UserID)
	}

	// Chown the range directories to the ludus user
	chownDirToUsernameRecursive(fmt.Sprintf("%s/ranges/", ludusInstallPath), "ludus")
}
