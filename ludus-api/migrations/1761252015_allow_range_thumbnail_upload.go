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
			&core.FileField{
				Name:      "thumbnail",
				Required:  false,
				MimeTypes: []string{"image/png", "image/jpeg", "image/gif", "image/webp"},
				MaxSize:   1024 * 1024 * 10, // 10MB
			},
		)

		return app.Save(rangesCollection)
	}, nil)
}
