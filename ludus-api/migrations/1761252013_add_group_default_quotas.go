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

		minZero := float64(0)

		// Add default quota fields to the groups collection
		groupsCollection.Fields.Add(
			&core.NumberField{
				Name:    "defaultQuotaRAM",
				OnlyInt: true,
				Min:     &minZero,
			},
			&core.NumberField{
				Name:    "defaultQuotaCPU",
				OnlyInt: true,
				Min:     &minZero,
			},
			&core.NumberField{
				Name:    "defaultQuotaVMs",
				OnlyInt: true,
				Min:     &minZero,
			},
			&core.NumberField{
				Name:    "defaultQuotaRanges",
				OnlyInt: true,
				Min:     &minZero,
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
