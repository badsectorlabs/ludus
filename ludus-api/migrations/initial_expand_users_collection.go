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

		// restrict the list, view, update, and delete rules for record owners
		usersCollection.ListRule = types.Pointer("id = @request.auth.id")
		usersCollection.ViewRule = types.Pointer("id = @request.auth.id")
		usersCollection.UpdateRule = types.Pointer("id = @request.auth.id")
		usersCollection.DeleteRule = types.Pointer("id = @request.auth.id")

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
				Name:     "dateLastActive",
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
		)

		// enable password auth and disable OTP
		usersCollection.PasswordAuth.Enabled = true
		usersCollection.OTP.Enabled = false

		return app.Save(usersCollection)
	}, nil)
}
