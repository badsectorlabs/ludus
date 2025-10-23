package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// init a new auth collection with the default system fields and auth options
		vmsCollection := core.NewBaseCollection("vms")

		// Only superusers can list, view, update, and delete VMs
		vmsCollection.ListRule = nil
		vmsCollection.ViewRule = nil
		vmsCollection.UpdateRule = nil
		vmsCollection.DeleteRule = nil

		vmsCollection.Fields.Add(
			&core.NumberField{
				Name:     "proxmoxID",
				Required: true,
				OnlyInt:  true,
			},
			&core.RelationField{
				Name:         "range",
				CollectionId: "ranges",
				Required:     true,
				MaxSelect:    1, // Only one range per VM
			},
			&core.TextField{
				Name:     "name",
				Required: true,
			},
			&core.BoolField{
				Name:     "poweredOn",
				Required: false,
			},
			&core.TextField{
				Name:     "ip",
				Required: false,
			},
			&core.BoolField{
				Name:     "isRouter",
				Required: false,
			},
			&core.AutodateField{
				Name:     "createdAt",
				OnCreate: true,
				OnUpdate: false,
			},
			&core.AutodateField{
				Name:     "updatedAt",
				OnCreate: true,
				OnUpdate: true,
			},
		)

		return app.Save(vmsCollection)
	}, func(app core.App) error { // optional revert operation
		vmsCollection, err := app.FindCollectionByNameOrId("vms")
		if err != nil {
			return err
		}

		return app.Delete(vmsCollection)
	})
}
