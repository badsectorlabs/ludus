package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// init a new auth collection with the default system fields and auth options
		groupsCollection := core.NewBaseCollection("groups")

		usersCollection, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}
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
				CollectionId: usersCollection.Id,
				Required:     false,
				MaxSelect:    9999, // Groups can have many managers
			},
			&core.RelationField{
				Name:         "members",
				CollectionId: usersCollection.Id,
				Required:     false,
				MaxSelect:    9999, // Groups can have many members
			},
			&core.RelationField{
				Name:         "ranges",
				CollectionId: rangesCollection.Id,
				Required:     false,
				MaxSelect:    9999, // Groups can have many ranges
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

		return app.Save(groupsCollection)
	}, func(app core.App) error { // optional revert operation
		groupsCollection, err := app.FindCollectionByNameOrId("groups")
		if err != nil {
			return err
		}

		return app.Delete(groupsCollection)
	})
}
