package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}

		rangesCollection.Fields.Add(
			&core.TextField{
				Name:     "inactivityShutdownTimeout",
				Required: false,
			},
			&core.DateField{
				Name:     "lastActive",
				Required: false,
			},
		)

		return app.Save(rangesCollection)
	}, func(app core.App) error {
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}

		rangesCollection.Fields.RemoveByName("inactivityShutdownTimeout")
		rangesCollection.Fields.RemoveByName("lastActive")

		return app.Save(rangesCollection)
	})
}
