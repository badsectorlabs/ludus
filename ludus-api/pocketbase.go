package ludusapi

import (
	"database/sql"
	"errors"
	"fmt"
	"ludusapi/models"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Returns the PocketBase ID of the created user
func createUserInPocketBase(txApp core.App, user *models.User, password string) (string, error) {
	logger.Debug(fmt.Sprintf("Creating user %s in PocketBase", user.Name))
	collection, err := txApp.FindCollectionByNameOrId("users")
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to find collection: %v", err))
	}
	record := core.NewRecord(collection)
	record.SetEmail(user.Email())
	record.SetPassword(password)
	record.Set("name", user.Name())
	record.Set("userID", user.UserId())
	record.Set("userNumber", user.UserNumber())
	record.Set("isAdmin", user.IsAdmin())
	record.Set("proxmoxUsername", user.ProxmoxUsername())
	record.Set("hashedAPIKey", user.HashedApikey())
	record.Set("proxmoxTokenID", user.ProxmoxTokenId())
	record.Set("proxmoxTokenSecret", user.ProxmoxTokenSecret())
	record.Set("dateLastActive", user.LastActive())
	record.Set("defaultRangeID", user.DefaultRangeId())

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

func getVMsForRange(rangeID string) ([]*models.VMs, error) {
	rangeRecord, err := app.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil {
		return nil, fmt.Errorf("error finding range: %w", err)
	}
	rangeObj := models.Range{}
	rangeObj.SetProxyRecord(rangeRecord)
	rangeVMs, err := app.FindAllRecords("vms", dbx.NewExp("range = {:rangeID}", dbx.Params{"rangeID": rangeObj.RangeId()}))
	if err != nil {
		return nil, fmt.Errorf("error finding VMs for range: %w", err)
	}
	var vms []*models.VMs
	for _, rangeVM := range rangeVMs {
		rangeVMObj := &models.VMs{}
		rangeVMObj.SetProxyRecord(rangeVM)
		vms = append(vms, rangeVMObj)
	}
	return vms, nil
}
