package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// init a new auth collection with the default system fields and auth options
		rangesCollection := core.NewBaseCollection("ranges")

		// Only superusers can list, view, update, and delete ranges
		rangesCollection.ListRule = nil
		rangesCollection.ViewRule = nil
		rangesCollection.UpdateRule = nil
		rangesCollection.DeleteRule = nil

		rangesCollection.Fields.Add(
			&core.NumberField{
				Name:     "rangeNumber",
				Required: true,
				OnlyInt:  true,
			},
			&core.TextField{
				Name:     "rangeID",
				Required: true,
				Pattern:  `^[A-Za-z][A-Za-z0-9_\-]*(\/[A-Za-z0-9_\-]+){0,2}$`,
			},
			&core.TextField{
				Name:     "name",
				Required: true,
			},
			&core.TextField{
				Name:     "description",
				Required: false,
			},
			&core.TextField{
				Name:     "purpose",
				Required: false,
			},
			&core.DateField{
				Name:     "lastDeployment",
				Required: false,
			},
			&core.NumberField{
				Name:     "numberOfVMs",
				Required: false,
				OnlyInt:  true,
			},
			&core.BoolField{
				Name:     "testingEnabled",
				Required: false,
			},
			&core.JSONField{
				Name:     "allowedDomains",
				Required: false,
			},
			&core.JSONField{
				Name:     "allowedIPs",
				Required: false,
			},
			&core.TextField{
				Name:     "rangeState",
				Required: false,
			},
			&core.AutodateField{
				Name:     "created",
				OnCreate: true,
				OnUpdate: false,
			},
			&core.AutodateField{
				Name:     "updated",
				OnCreate: true,
				OnUpdate: true,
			},
		)

		return app.Save(rangesCollection)
	}, func(app core.App) error { // optional revert operation
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}

		return app.Delete(rangesCollection)
	})
}
