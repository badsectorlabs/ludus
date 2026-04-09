package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		groupsCollection, err := app.FindCollectionByNameOrId("groups")
		if err != nil {
			return err
		}

		// Add default quota fields to the groups collection
		groupsCollection.Fields.Add(
			&core.NumberField{
				Name:    "defaultQuotaRAM",
				OnlyInt: true,
				Min:     func() *float64 { v := float64(0); return &v }(),
			},
			&core.NumberField{
				Name:    "defaultQuotaCPU",
				OnlyInt: true,
				Min:     func() *float64 { v := float64(0); return &v }(),
			},
			&core.NumberField{
				Name:    "defaultQuotaVMs",
				OnlyInt: true,
				Min:     func() *float64 { v := float64(0); return &v }(),
			},
			&core.NumberField{
				Name:    "defaultQuotaRanges",
				OnlyInt: true,
				Min:     func() *float64 { v := float64(0); return &v }(),
			},
		)

		return app.Save(groupsCollection)
	}, func(app core.App) error {
		groupsCollection, err := app.FindCollectionByNameOrId("groups")
		if err != nil {
			return err
		}

		groupsCollection.Fields.RemoveByName("defaultQuotaRAM")
		groupsCollection.Fields.RemoveByName("defaultQuotaCPU")
		groupsCollection.Fields.RemoveByName("defaultQuotaVMs")
		groupsCollection.Fields.RemoveByName("defaultQuotaRanges")

		return app.Save(groupsCollection)
	})
}
