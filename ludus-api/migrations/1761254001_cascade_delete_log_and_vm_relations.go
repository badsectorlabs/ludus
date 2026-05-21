package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		logsCollection, err := app.FindCollectionByNameOrId("logs")
		if err != nil {
			return err
		}
		for _, name := range []string{"user", "range"} {
			if f, ok := logsCollection.Fields.GetByName(name).(*core.RelationField); ok {
				f.CascadeDelete = true
			}
		}
		if err := app.Save(logsCollection); err != nil {
			return err
		}

		vmsCollection, err := app.FindCollectionByNameOrId("vms")
		if err != nil {
			return err
		}
		if f, ok := vmsCollection.Fields.GetByName("range").(*core.RelationField); ok {
			f.CascadeDelete = true
		}
		return app.Save(vmsCollection)
	}, func(app core.App) error {
		logsCollection, err := app.FindCollectionByNameOrId("logs")
		if err != nil {
			return err
		}
		for _, name := range []string{"user", "range"} {
			if f, ok := logsCollection.Fields.GetByName(name).(*core.RelationField); ok {
				f.CascadeDelete = false
			}
		}
		if err := app.Save(logsCollection); err != nil {
			return err
		}

		vmsCollection, err := app.FindCollectionByNameOrId("vms")
		if err != nil {
			return err
		}
		if f, ok := vmsCollection.Fields.GetByName("range").(*core.RelationField); ok {
			f.CascadeDelete = false
		}
		return app.Save(vmsCollection)
	})
}
