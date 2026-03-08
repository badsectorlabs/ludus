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
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}
		groupsCollection, err := app.FindCollectionByNameOrId("groups")
		if err != nil {
			return err
		}
		usersCollection.Fields.Add(
			&core.RelationField{
				Name:         "ranges", // Ranges the user has access to
				CollectionId: rangesCollection.Id,
				Required:     false,
				MaxSelect:    9999, // Users can have access to many ranges
			},
			&core.RelationField{
				Name:         "groups", // Groups the user is a member of
				CollectionId: groupsCollection.Id,
				Required:     false,
				MaxSelect:    9999, // Users can be a member of many groups
			},
		)

		return app.Save(usersCollection)
	}, nil)
}
