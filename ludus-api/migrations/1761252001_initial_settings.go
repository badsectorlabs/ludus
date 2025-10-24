package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		settings := app.Settings()

		// for all available settings fields see
		// https://github.com/pocketbase/pocketbase/blob/develop/core/settings_model.go#L121-L130
		settings.Meta.AppName = "Ludus"
		settings.Meta.AppURL = "https://my.ludus.cloud:8080"
		settings.Logs.MaxDays = 7
		settings.Logs.LogAuthId = true
		settings.Logs.LogIP = true
		settings.TrustedProxy.Headers = []string{"X-Forwarded-For"}

		return app.Save(settings)
	}, nil)
}
