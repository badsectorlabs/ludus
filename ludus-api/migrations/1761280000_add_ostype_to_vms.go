package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		vmsCollection, err := app.FindCollectionByNameOrId("vms")
		if err != nil {
			return err
		}

		vmsCollection.Fields.Add(
			&core.TextField{
				Name:     "osType",
				Required: false,
			},
		)

		return app.Save(vmsCollection)
	}, func(app core.App) error {
		// Revert: remove the osType field
		vmsCollection, err := app.FindCollectionByNameOrId("vms")
		if err != nil {
			return err
		}

		vmsCollection.Fields.RemoveById(vmsCollection.Fields.GetByName("osType").GetId())

		return app.Save(vmsCollection)
	})
}
