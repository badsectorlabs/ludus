package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// Expand the default user collection with relevant fields
		usersCollection, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		// Only superusers can list, view, update, and delete users
		usersCollection.ListRule = nil
		usersCollection.ViewRule = nil
		usersCollection.CreateRule = nil
		usersCollection.UpdateRule = nil
		usersCollection.DeleteRule = nil

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
			&core.BoolField{
				Name:     "hasCompletedOnboarding",
				Required: false,
			},
		)

		// enable password auth and disable OTP
		usersCollection.PasswordAuth.Enabled = true
		usersCollection.OTP.Enabled = false

		return app.Save(usersCollection)
	}, nil)
}
