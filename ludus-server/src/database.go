package ludusapi

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var (
	once     sync.Once
	db       *gorm.DB
	database string = fmt.Sprintf("%s/ludus.db", ludusInstallPath)
)

func InitDb() *gorm.DB {
	// Only initialize and open the DB once per run
	once.Do(func() {
		var err error

		db, err = gorm.Open(sqlite.Open(database), &gorm.Config{
			SkipDefaultTransaction: true,
		})
		if err != nil {
			log.Fatalf("error opening db: %v", err)
		}

		// Error
		if err != nil {
			panic(err)
		}
		// Create the tables if they don't exist and we are root
		if !db.Migrator().HasTable(&UserObject{}) && os.Geteuid() == 0 {
			db.Set("gorm:table_options", "ENGINE=InnoDB")
			db.Raw("PRAGMA journal_mode = WAL")
			db.Migrator().CreateTable(&UserObject{})
			db.Migrator().CreateTable(&RangeObject{})
			db.Migrator().CreateTable(&VmObject{})

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
			usersRange.UserID = user.UserID
			usersRange.NumberOfVMs = 0
			usersRange.TestingEnabled = false
			db.Create(&usersRange)
		}
		// Only do migrations as ludus-admin service to prevent a race condition when starting services
		// that leads to the ludus service creating the tables via migration before the root api key
		// has been written
		if os.Geteuid() == 0 {
			// Migrate any updates from the models to an existing DB
			db.AutoMigrate(&UserObject{}, &RangeObject{}, &VmObject{})
		}
	})

	return db
}

// findNextAvailableRangeNumber finds the smallest positive integer that is not
// present in the RangeNumber column of the RangeObject table. This function
// assumes that the RangeNumber values are positive integers and that there can
// be gaps or non-sequential values in the column.
//
// The function takes a *gorm.DB as an argument, which should be a valid GORM
// database connection.
//
// Returns:
//
//	int32 - The smallest positive integer that is not present in the
//	        RangeNumber column of the RangeObject table.
func findNextAvailableRangeNumber(db *gorm.DB) int32 {
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
			return i
		}
	}
}
