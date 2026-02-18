package ludusapi

import (
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"

	_ "ludusapi/migrations"
	"ludusapi/models"
)

func InitDb() {
	if os.Geteuid() == 0 {
		// If a root-api-key file doesn't exist, recreate the root user in the database
		if !FileExists(fmt.Sprintf("%s/install/root-api-key", ludusInstallPath)) {
			logger.Info("Creating root user in database")
			createRootUserInDatabase()
		}
		// Check if there was a previous sqlite db, and if so, run the migrations
		if FileExists(fmt.Sprintf("%s/ludus.db", ludusInstallPath)) && !FileExists(fmt.Sprintf("%s/install/.sqlite_db_migrated", ludusInstallPath)) {
			slog.Info("SQLite database found, running migrations")
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
	user.SetPassword(security.RandomString(25)) // Will never be used, but needed to create the record

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
