package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// init a new auth collection with the default system fields and auth options
		vmsCollection := core.NewBaseCollection("vms")

		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}

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
				CollectionId: rangesCollection.Id,
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
				Name:     "created",
				OnCreate: true,
				OnUpdate: false,
			},
			&core.AutodateField{
				Name:     "updated",
				OnCreate: true,
				OnUpdate: true,
			},
			&core.NumberField{
				Name:     "cpu",
				Required: false,
				OnlyInt:  true,
				Min:      nil,
				Max:      nil,
			},
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

		return app.Delete(vmsCollection)
	})
}
