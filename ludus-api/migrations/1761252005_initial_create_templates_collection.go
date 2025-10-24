package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// init a new auth collection with the default system fields and auth options
		templatesCollection := core.NewBaseCollection("templates")

		usersCollection, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		// Only superusers can list, view, update, and delete templates
		templatesCollection.ListRule = nil
		templatesCollection.ViewRule = nil
		templatesCollection.UpdateRule = nil
		templatesCollection.DeleteRule = nil

		templatesCollection.Fields.Add(
			&core.TextField{
				Name:     "name",
				Required: true,
			},
			&core.SelectField{
				Name:   "os",
				Values: []string{"linux", "windows", "macos", "other"},
			},
			&core.FileField{
				Name:      "icon",
				Required:  false,
				MimeTypes: []string{"image/png", "image/jpeg", "image/gif", "image/webp"},
				MaxSize:   1024 * 1024 * 10, // 10MB
			},
			&core.TextField{
				Name:     "description",
				Required: false,
			},
			&core.RelationField{
				Name:         "owner",
				CollectionId: usersCollection.Id,
				Required:     false, // A template must have an owner (optional for system templates)
				MaxSelect:    1,     // Only one owner per template
			},
			&core.BoolField{
				Name:     "shared",
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

		return app.Save(templatesCollection)
	}, func(app core.App) error { // optional revert operation
		templatesCollection, err := app.FindCollectionByNameOrId("templates")
		if err != nil {
			return err
		}

		return app.Delete(templatesCollection)
	})
}
