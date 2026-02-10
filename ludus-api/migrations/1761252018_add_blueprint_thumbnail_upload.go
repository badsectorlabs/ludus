package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		blueprintsCollection, err := app.FindCollectionByNameOrId("blueprints")
		if err != nil {
			return err
		}

		if blueprintsCollection.Fields.GetByName("thumbnail") == nil {
			blueprintsCollection.Fields.Add(
				&core.FileField{
					Name:      "thumbnail",
					Required:  false,
					MaxSelect: 1,
					MimeTypes: []string{
						"image/png",
						"image/jpeg",
						"image/gif",
						"image/webp",
					},
					MaxSize: 1024 * 1024 * 10, // 10MB
				},
			)
		}

		return app.Save(blueprintsCollection)
	}, func(app core.App) error {
		blueprintsCollection, err := app.FindCollectionByNameOrId("blueprints")
		if err != nil {
			return err
		}

		blueprintsCollection.Fields.RemoveByName("thumbnail")
		return app.Save(blueprintsCollection)
	})
}
