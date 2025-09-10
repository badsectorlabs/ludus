package ludusapi

import (
	"fmt"
	"log"
	"os"
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
			usersRange.RangeID = "ROOT"
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
			err := db.AutoMigrate(&UserObject{}, &RangeObject{}, &VmObject{},
				&GroupObject{}, &UserRangeAccess{}, &UserGroupMembership{}, &GroupRangeAccess{})
			if err != nil {
				log.Fatalf("error migrating database: %v", err)
			}

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

	for i := int32(1); ; i++ {
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
