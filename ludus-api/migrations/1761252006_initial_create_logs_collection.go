package migrations

import (
	"github.com/pocketbase/pocketbase/core"

	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// init a new auth collection with the default system fields and auth options
		logsCollection := core.NewBaseCollection("logs")

		usersCollection, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		rangesCollection, err := app.FindCollectionByNameOrId("ranges")
		if err != nil {
			return err
		}
		templatesCollection, err := app.FindCollectionByNameOrId("templates")
		if err != nil {
			return err
		}
		// Only superusers can list, view, update, and delete logs
		logsCollection.ListRule = nil
		logsCollection.ViewRule = nil
		logsCollection.UpdateRule = nil
		logsCollection.DeleteRule = nil

		logsCollection.Fields.Add(
			&core.RelationField{
				Name:         "user", // User who triggered the log
				CollectionId: usersCollection.Id,
				Required:     true, // A user must have started the action (root for system actions)
				MaxSelect:    1,    // Only one user per log
			},
			&core.RelationField{
				Name:         "range", // Range the log is about
				CollectionId: rangesCollection.Id,
				Required:     false,
				MaxSelect:    1, // Only one range per log
			},
			&core.RelationField{
				Name:         "template", // Template the log is about
				CollectionId: templatesCollection.Id,
				Required:     false,
				MaxSelect:    1, // Only one template per log
			},
			&core.SelectField{
				Name:   "status",
				Values: []string{"success", "failure", "running", "aborted"},
			},
			&core.DateField{
				Name:     "start",
				Required: false,
			},
			&core.DateField{
				Name:     "end",
				Required: false,
			},
			&core.FileField{
				Name:     "log",
				Required: false,
				MaxSize:  1024 * 1024 * 20, // 20MB
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

		return app.Save(logsCollection)
	}, func(app core.App) error { // optional revert operation
		logsCollection, err := app.FindCollectionByNameOrId("logs")
		if err != nil {
			return err
		}

		return app.Delete(logsCollection)
	})
}
