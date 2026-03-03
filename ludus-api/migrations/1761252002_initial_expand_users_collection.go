package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// Expand the default user collection with relevant fields
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

		// add fields to match the UserObject model
		usersCollection.Fields.Add(
			&core.TextField{
				Name:     "userID",
				Required: true,
				Pattern:  "^[A-Za-z0-9]{1,20}$",
			},
			&core.NumberField{
				Name:     "userNumber",
				Required: true,
				OnlyInt:  true,
			},
			&core.DateField{
				Name:     "lastActive",
				Required: false,
			},
			&core.BoolField{
				Name:     "isAdmin",
				Required: false,
			},
			&core.TextField{
				Name:     "hashedAPIKey",
				Required: false,
				Hidden:   true,
			},
			&core.TextField{
				Name:     "proxmoxUsername",
				Required: false,
			},
			&core.TextField{
				Name:     "proxmoxPassword",
				Required: false,
				Hidden:   true,
			},
			&core.TextField{
				Name:     "proxmoxRealm",
				Required: false,
			},
			&core.TextField{
				Name:     "proxmoxTokenID",
				Required: false,
				Hidden:   true,
			},
			&core.TextField{
				Name:     "proxmoxTokenSecret",
				Required: false,
				Hidden:   true,
			},
			&core.NumberField{
				Name:     "quotaRAM",
				Required: false,
				OnlyInt:  true,
			},
			&core.NumberField{
				Name:     "quotaCPU",
				Required: false,
				OnlyInt:  true,
			},
			&core.NumberField{
				Name:     "quotaStorage",
				Required: false,
				OnlyInt:  true,
			},
			&core.NumberField{
				Name:     "quotaVMs",
				Required: false,
				OnlyInt:  true,
			},
			&core.NumberField{
				Name:     "quotaRanges",
				Required: false,
				OnlyInt:  true,
			},
			&core.TextField{
				Name:     "defaultRangeID",
				Required: false,
			},
		)

		// enable password auth and disable OTP
		usersCollection.PasswordAuth.Enabled = true
		usersCollection.OTP.Enabled = false

		return app.Save(usersCollection)
	}, nil)
}
