package ludusapi

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
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

		newLogger := gormlogger.New(
			log.New(os.Stdout, "[DATABASE] ", log.LstdFlags),
			gormlogger.Config{
				SlowThreshold:             time.Second,
				LogLevel:                  gormlogger.Warn,
				IgnoreRecordNotFoundError: true,
				Colorful:                  true,
			},
		)

		db, err = gorm.Open(postgres.Open(databaseURL), &gorm.Config{
			SkipDefaultTransaction: true,
			Logger:                 newLogger,
		})
		if err != nil {
			// Check if there was a previous sqlite db, and if so, run the setup-db-container.yml to migrate to postgres
			if FileExists(fmt.Sprintf("%s/ludus.db", ludusInstallPath)) && !FileExists(fmt.Sprintf("%s/install/.sqlite_db_migrated", ludusInstallPath)) {
				slog.Info("SQLite database found, running setup-db-container.yml, this will take a minute or two...")
				output, err := exec.Command("ansible-playbook", "-i", "localhost", fmt.Sprintf("%s/ansible/proxmox-install/tasks/setup-db-container.yml", ludusInstallPath)).CombinedOutput()
				slog.Debug(string(output))
				if err != nil {
					log.Fatalf("error running ansible-playbook: %v", err)
				}
				// Open the database connection again
				db, err = gorm.Open(postgres.Open(databaseURL), &gorm.Config{
					SkipDefaultTransaction: true,
					Logger:                 newLogger,
				})
				if err != nil {
					log.Fatalf("error opening db after db setup: %v", err)
				}
			} else {
				log.Fatalf("error opening db: %v", err)
			}
		}

		// Create the tables if they don't exist and we are root
		if !db.Migrator().HasTable(&UserObject{}) && os.Geteuid() == 0 {

			logger.Info("Creating tables in PostgreSQL")

			db.Migrator().CreateTable(&UserObject{})
			db.Migrator().CreateTable(&RangeObject{})
			db.Migrator().CreateTable(&VmObject{})
			db.Migrator().CreateTable(&GroupObject{})
			db.Migrator().CreateTable(&UserRangeAccess{})
			db.Migrator().CreateTable(&UserGroupMembership{})
			db.Migrator().CreateTable(&GroupRangeAccess{})

			logger.Info("Creating root user in database")
			createRootUserInDatabase()
		}
		// Only do migrations as ludus-admin service to prevent a race condition when starting services
		// that leads to the ludus service creating the tables via migration before the root api key
		// has been written
		if os.Geteuid() == 0 {
			// Migrate any updates from the models to an existing DB
			err := db.AutoMigrate(&UserObject{}, &RangeObject{}, &VmObject{},
				&GroupObject{}, &UserRangeAccess{}, &UserGroupMembership{}, &GroupRangeAccess{})
			if err != nil {
				log.Fatalf("error migrating database: %v", err)
			}

			// Attempt to migrate from SQLite if conditions are met
			if err := MigrateFromSQLite(); err != nil {
				log.Printf("Warning: SQLite migration failed: %v", err)
			}

			// If a root-api-key file doesn't exist, recreate the root user in the database
			if !FileExists(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath)) {
				logger.Info("Recreating root user in database")
				createRootUserInDatabase()
			}
		}
	})

	return db
}

func createRootUserInDatabase() {

	// Check if the root user already exists in the database
	var rootUser UserObject
	db.First(&rootUser, "user_id = ?", "ROOT")
	if rootUser.UserID != "" {
		logger.Info("Root user already exists in database, removing it")
		db.Delete(&rootUser)
	}

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
	tokenID, tokenSecret, err := createRootAPITokenWithShell()
	if err != nil {
		log.Fatal(err.Error())
	}

	encryptedTokenSecret, err := EncryptStringForDatabase(tokenSecret)
	if err != nil {
		log.Fatal("error encrypting Root API Token secret")
	}

	user.ProxmoxTokenID = tokenID
	user.ProxmoxTokenSecret = encryptedTokenSecret

	os.MkdirAll(fmt.Sprintf("%s/users/root", ludusInstallPath), 0700)

	user.HashedAPIKey, err = HashString(apiKey)
	if err != nil {
		log.Fatal("error hashing API Key for root user")
	}
	db.Create(&user)
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

// findNextAvailableUserNumber finds the smallest positive integer that is not
// present in the UserNumber column of the UserObject table. This function
// assumes that the UserNumber values are positive integers and that there can
// be gaps or non-sequential values in the column.
//
// The function takes a *gorm.DB as an argument, which should be a valid GORM
// database connection.
//
// Returns:
//
//	int32 - The smallest positive integer that is not present in the
//	        UserNumber column of the UserObject table.
func findNextAvailableUserNumber(db *gorm.DB) int32 {
	var userNumbers []int32
	db.Model(&UserObject{}).Select("user_number").Order("user_number").Scan(&userNumbers)

	// Start at 2 since 1 is reserved for the root user (198.51.100.1 is reserved for the server)
	for i := int32(2); ; i++ {
		found := false
		for _, num := range userNumbers {
			if num == i {
				found = true
				break
			}
		}
		if !found {
			return i
		}
	}
}
