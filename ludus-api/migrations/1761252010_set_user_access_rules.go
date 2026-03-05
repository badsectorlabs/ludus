package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		usersCollection, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		// Allow access if the user is authenticated and is the user themselves or an Admin.
		isThisUserRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true ||
			@request.auth.id = id
		)`)

		// Allow access if the user is authenticated and are changing their own user data. Admins always have access.
		// We also check explicitly that the only fields that are allowed to be changed are: name, avatar, email, hasCompletedOnboarding
		// This is to prevent users from changing other fields that are not allowed to be changed directly via the API
		allowSelfEditRule := types.Pointer(`
		@request.auth.id != "" && (
			@request.auth.isAdmin = true ||
			@request.auth.id = id
		) &&
		@request.body.emailVisibility:changed = false &&
		@request.body.verified:changed = false &&
		@request.body.isAdmin:changed = false &&
		@request.body.proxmoxTokenID:changed = false &&
		@request.body.proxmoxTokenSecret:changed = false &&
		@request.body.proxmoxUsername:changed = false &&
		@request.body.proxmoxRealm:changed = false &&
		@request.body.quotaRAM:changed = false &&
		@request.body.quotaCPU:changed = false &&
		@request.body.quotaStorage:changed = false &&
		@request.body.quotaVMs:changed = false &&
		@request.body.quotaRanges:changed = false &&
		@request.body.created:changed = false &&
		@request.body.updated:changed = false &&
		@request.body.lastActive:changed = false && 
		@request.body.userID:changed = false &&
		@request.body.userNumber:changed = false &&
		@request.body.hashedAPIKey:changed = false &&
		@request.body.ranges:changed = false &&
		@request.body.groups:changed = false &&
		@request.body.verified:changed = false &&
		@request.body.defaultRangeID:changed = false
		`)
		usersCollection.ListRule = isThisUserRule
		usersCollection.ViewRule = isThisUserRule
		usersCollection.UpdateRule = allowSelfEditRule
		usersCollection.DeleteRule = nil // Use the ludus handler for these operations
		usersCollection.CreateRule = nil

		return app.Save(usersCollection)
	}, nil)
}
