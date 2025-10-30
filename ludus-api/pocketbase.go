package ludusapi

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// Returns the PocketBase ID of the created user
func createUserInPocketBase(txApp core.App, user UserWithEmailAndPassword, password string) (string, error) {
	logger.Debug(fmt.Sprintf("Creating user %s in PocketBase", user.Name))
	collection, err := txApp.FindCollectionByNameOrId("users")
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to find collection: %v", err))
	}
	record := core.NewRecord(collection)
	record.SetEmail(user.Email)
	record.SetPassword(password)
	record.Set("name", user.UserObject.Name)
	record.Set("userID", user.UserObject.UserID)
	record.Set("userNumber", user.UserObject.UserNumber)
	record.Set("isAdmin", user.UserObject.IsAdmin)
	record.Set("proxmoxUsername", user.UserObject.ProxmoxUsername)
	record.Set("hashedAPIKey", user.UserObject.HashedAPIKey)
	record.Set("proxmoxTokenID", user.UserObject.ProxmoxTokenID)
	record.Set("proxmoxTokenSecret", user.UserObject.ProxmoxTokenSecret)
	record.Set("dateLastActive", user.UserObject.DateLastActive)

	if err := txApp.Save(record); err != nil {
		logger.Error(fmt.Sprintf("Failed to create user: %v", err))
	}

	logger.Info(fmt.Sprintf("Successfully created PocketBase user with ID: %s", record.Id))

	return record.Id, nil
}

func removeUserFromPocketBaseByUserID(userID string) error {
	// Get the user's PocketBase ID from the database
	record, err := app.FindFirstRecordByData("users", "userID", userID)
	if err != nil {
		return fmt.Errorf("user %s not found in database: %w", userID, err)
	}

	return removeUserFromPocketBaseByID(record.Id)
}

func removeUserFromPocketBaseByID(pocketbaseID string) error {
	record, err := app.FindRecordById("users", pocketbaseID)
	if err != nil {
		// It's good practice to check if the error is because the record was not found.
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("user with PocketBase ID %q not found, nothing to delete", pocketbaseID)
		}
		// Another error occurred (e.g., database connection issue).
		return fmt.Errorf("failed to find user: %w", err)
	}

	logger.Debug(fmt.Sprintf("Found user record with ID: %s. Proceeding with deletion...\n", record.Id))

	// 2. Delete the retrieved record.
	// This will also trigger any configured cascade-delete operations.
	if err := app.Delete(record); err != nil {
		return fmt.Errorf("failed to delete user record: %w", err)
	}

	return nil
}
