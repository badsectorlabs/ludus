package ludusapi

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/supabase-community/auth-go/types"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	once sync.Once
	db   *gorm.DB
)

func InitDb() *gorm.DB {
	// Only initialize and open the DB once per run
	once.Do(func() {
		var err error

		// Use PostgreSQL connection string from configuration
		databaseURL := ServerConfiguration.DatabaseURL
		if databaseURL == "" {
			databaseURL = "postgres://postgres.your-tenant-id:your-super-secret-and-long-postgres-password@192.0.2.1:5432/postgres"
		}

		db, err = gorm.Open(postgres.Open(databaseURL), &gorm.Config{
			SkipDefaultTransaction: true,
		})
		if err != nil {
			log.Fatalf("error opening db: %v", err)
		}

		// Create the tables if they don't exist and we are root
		if !db.Migrator().HasTable(&UserObject{}) && os.Geteuid() == 0 {
			db.Migrator().CreateTable(&UserObject{})
			db.Migrator().CreateTable(&RangeObject{})
			db.Migrator().CreateTable(&VmObject{})
			db.Migrator().CreateTable(&GroupObject{})
			db.Migrator().CreateTable(&UserRangeAccess{})
			db.Migrator().CreateTable(&UserGroupMembership{})
			db.Migrator().CreateTable(&GroupRangeAccess{})

			// Create a root user
			var user UserObject
			user.Name = "root"
			user.ProxmoxUsername = "root"
			user.UserID = "ROOT"
			user.DateCreated = time.Now()
			user.DateLastActive = time.Now()
			user.IsAdmin = true
			apiKey := GenerateAPIKey(&user)
			err := os.WriteFile(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath), []byte(apiKey), 0400)
			if err != nil {
				log.Fatal(err.Error())
			}
			os.Chown(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath), 0, 0)

			os.MkdirAll(fmt.Sprintf("%s/users/root", ludusInstallPath), 0700)

			user.HashedAPIKey, err = HashString(apiKey)
			if err != nil {
				log.Fatal("error hashing API Key for root user")
			}
			db.Create(&user)

			// Create a dummy range for ROOT to take up the range_id of 1 (since the server has the .1 address)
			var usersRange RangeObject
			usersRange.Name = "Root System Range"
			usersRange.Description = "System range for root user operations"
			usersRange.Purpose = "System administration and management"
			usersRange.UserID = user.UserID
			usersRange.RangeNumber = 1
			usersRange.NumberOfVMs = 0
			usersRange.RangeState = "NEVER DEPLOYED"
			db.Create(&usersRange)

			// Create UserRangeAccess record for root user's default range
			var userRangeAccess UserRangeAccess
			userRangeAccess.UserID = user.UserID
			userRangeAccess.RangeNumber = usersRange.RangeNumber
			db.Create(&userRangeAccess)
		}
		// Only do migrations as ludus-admin service to prevent a race condition when starting services
		// that leads to the ludus service creating the tables via migration before the root api key
		// has been written
		if os.Geteuid() == 0 {
			// Migrate any updates from the models to an existing DB
			db.AutoMigrate(&UserObject{}, &RangeObject{}, &VmObject{},
				&GroupObject{}, &UserRangeAccess{}, &UserGroupMembership{}, &GroupRangeAccess{})

			// Attempt to migrate from SQLite if conditions are met
			if err := MigrateFromSQLite(); err != nil {
				log.Printf("Warning: SQLite migration failed: %v", err)
			}
		}
	})

	return db
}

// findNextAvailableRangeNumber finds the smallest positive integer that is not
// present in the RangeNumber column of the RangeObject table. This function
// assumes that the RangeNumber values are positive integers and that there can
// be gaps or non-sequential values in the column. It will also skip any values
// in the reserved_range_numbers array in the config.
//
// The function takes a *gorm.DB as an argument, which should be a valid GORM
// database connection.
//
// Returns:
//
//	int32 - The smallest positive integer that is not present in the
//	        RangeNumber column of the RangeObject table.
func findNextAvailableRangeNumber(db *gorm.DB, reservedRangeNumbers []int32) int32 {
	var rangeNumbers []int32
	db.Model(&RangeObject{}).Select("range_number").Order("range_number").Scan(&rangeNumbers)

	for i := int32(1); ; i++ {
		found := false
		for _, num := range rangeNumbers {
			if num == i {
				found = true
				break
			}
		}
		if !found {
			// Check if this is a reserved range number
			for _, num := range reservedRangeNumbers {
				if num == i {
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

// MigrateFromSQLite migrates data from SQLite to PostgreSQL if conditions are met
func MigrateFromSQLite() error {
	sqlitePath := fmt.Sprintf("%s/ludus.db", ludusInstallPath)

	// Check if SQLite database exists
	if _, err := os.Stat(sqlitePath); os.IsNotExist(err) {
		log.Println("SQLite database not found, skipping migration")
		return nil
	}

	// Check if PostgreSQL database only has ROOT user
	var userCount int64
	if err := db.Model(&UserObject{}).Count(&userCount).Error; err != nil {
		return fmt.Errorf("error checking user count: %v", err)
	}

	if userCount > 1 {
		log.Println("PostgreSQL database has more than ROOT user, skipping migration")
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

		// Check if user already exists in PostgreSQL
		var existingUser UserObject
		if err := tx.Where("user_id = ?", sqliteUser.UserID).First(&existingUser).Error; err == nil {
			log.Printf("User %s already exists in PostgreSQL, skipping", sqliteUser.UserID)
			continue
		}

		// Create PostgreSQL user object
		user := UserObject{
			Name:                  sqliteUser.Name,
			UserID:                sqliteUser.UserID,
			DateCreated:           sqliteUser.DateCreated,
			DateLastActive:        sqliteUser.DateLastActive,
			IsAdmin:               sqliteUser.IsAdmin,
			HashedAPIKey:          sqliteUser.HashedAPIKey,
			ProxmoxUsername:       sqliteUser.ProxmoxUsername,
			PortforwardingEnabled: sqliteUser.PortforwardingEnabled,
			CreatedAt:             time.Now(),
			UpdatedAt:             time.Now(),
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
			continue // Skip ROOT's range as it already exists in PostgreSQL
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
			UserID:         sqliteRange.UserID,
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
		log.Printf("Migrated range: %d (User: %s)", rangeObj.RangeNumber, rangeObj.UserID)

		// Create UserRangeAccess record for the range owner
		userRangeAccess := UserRangeAccess{
			UserID:      rangeObj.UserID,
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
			if err := tx.Where("user_id = ?", rangeAccess.TargetUserID).First(&targetRange).Error; err != nil {
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
				password, err := os.ReadFile(fmt.Sprintf("%s/users/%s/proxmox_password", ludusInstallPath, username))
				if err != nil {
					log.Printf("Error reading proxmox password for user %s: %v", username, err)
					continue
				}
				// Lookup the user in the database
				var user UserObject
				if err := db.Where("proxmox_username = ?", username).First(&user).Error; err != nil {
					log.Printf("Error looking up user %s in database: %v", username, err)
					continue
				}

				userWithEmailAndPassword := UserWithEmailAndPassword{
					UserObject: user,
					Password:   string(password),
					Email:      user.ProxmoxUsername + "@ludus.localhost",
				}

				var supabaseUser types.User
				if user.UUID == uuid.Nil {
					supabaseUser, err = createUserInSupabase(userWithEmailAndPassword, string(password))
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
