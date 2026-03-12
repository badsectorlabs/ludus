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

		// Enforce uniqueness at the DB level to prevent duplicate range IDs or numbers.
		rangesCollection.AddIndex("idx_ranges_rangeID_unique", true, "`rangeID`", "")
		rangesCollection.AddIndex("idx_ranges_rangeNumber_unique", true, "`rangeNumber`", "")

		return app.Save(rangesCollection)

	}, func(app core.App) error {
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}

		rangesCollection.RemoveIndex("idx_ranges_rangeID_unique")
		rangesCollection.RemoveIndex("idx_ranges_rangeNumber_unique")
		return app.Save(rangesCollection)
	})
}
