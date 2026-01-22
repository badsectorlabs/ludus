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

		// Add CPU field (number of CPU cores)
		vmsCollection.Fields.Add(
			&core.NumberField{
				Name:     "cpu",
				Required: false,
				OnlyInt:  true,
				Min:      nil,
				Max:      nil,
			},
		)

		// Add RAM field (in GB)
		vmsCollection.Fields.Add(
			&core.NumberField{
				Name:     "ram",
				Required: false,
				OnlyInt:  true,
				Min:      nil,
				Max:      nil,
			},
		)

		return app.Save(vmsCollection)
	}, func(app core.App) error { // optional revert operation
		vmsCollection, err := app.FindCollectionByNameOrId("vms")
		if err != nil {
			return err
		}

		// Remove the cpu and ram fields
		vmsCollection.Fields.RemoveByName("cpu")
		vmsCollection.Fields.RemoveByName("ram")

		return app.Save(vmsCollection)
	})
}
