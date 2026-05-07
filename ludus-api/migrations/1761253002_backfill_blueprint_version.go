package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

// Backfill blueprint.version for legacy records that pre-date the
// version field. The bundle manifest requires semver, and the create
// handler now refuses to write empty version, so any "" surviving here
// must be a pre-source-management record.
func init() {
	m.Register(func(app core.App) error {
		records, err := app.FindAllRecords("blueprints")
		if err != nil {
			return err
		}
		for _, rec := range records {
			if rec.GetString("version") == "" {
				rec.Set("version", "1.0.0")
				if err := app.Save(rec); err != nil {
					return err
				}
			}
		}
		return nil
	}, func(app core.App) error {
		return nil
	})
}
