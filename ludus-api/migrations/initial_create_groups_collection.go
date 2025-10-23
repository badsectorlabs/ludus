package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// init a new auth collection with the default system fields and auth options
		groupsCollection := core.NewBaseCollection("groups")

		// Only superusers can list, view, update, and delete VMs
		groupsCollection.ListRule = nil
		groupsCollection.ViewRule = nil
		groupsCollection.UpdateRule = nil
		groupsCollection.DeleteRule = nil

		groupsCollection.Fields.Add(
			&core.TextField{
				Name:     "name",
				Required: true,
			},
			&core.TextField{
				Name:     "description",
				Required: false,
			},
			&core.RelationField{
				Name:         "managers",
				CollectionId: "users",
				Required:     false,
			},
			&core.RelationField{
				Name:         "members",
				CollectionId: "users",
				Required:     false,
			},
			&core.RelationField{
				Name:         "ranges",
				CollectionId: "ranges",
				Required:     false,
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

		return app.Save(groupsCollection)
	}, func(app core.App) error { // optional revert operation
		groupsCollection, err := app.FindCollectionByNameOrId("groups")
		if err != nil {
			return err
		}

		return app.Delete(groupsCollection)
	})
}
