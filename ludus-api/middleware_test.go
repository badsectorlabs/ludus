package ludusapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestAdminRoutingPreservesPocketBaseAuthorization(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("admin routing requires the root service identity")
	}
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger = previousLogger })

	superuser := core.NewRecord(core.NewAuthCollection(core.CollectionNameSuperusers))
	user := core.NewRecord(core.NewAuthCollection("users"))
	for _, test := range []struct {
		name   string
		path   string
		auth   *core.Record
		status int
	}{
		{"plugin superuser reaches PocketBase", "/api/collections/users/records", superuser, http.StatusOK},
		{"missing native API authentication", "/api/collections/users/records", nil, http.StatusUnauthorized},
		{"ordinary user lacks superuser access", "/api/collections/users/records", user, http.StatusForbidden},
		{"Ludus route remains on regular service", APIBasePath + "/kms/status", superuser, http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			event := &core.RequestEvent{
				Auth: test.auth,
				Event: router.Event{
					Request:  httptest.NewRequest(http.MethodGet, test.path, nil),
					Response: response,
				},
			}
			chain := hook.Hook[*core.RequestEvent]{}
			chain.BindFunc(limitRootEndpoints)
			chain.Bind(apis.RequireSuperuserAuth())
			err := chain.Trigger(event, func(e *core.RequestEvent) error {
				return e.JSON(http.StatusOK, map[string]bool{"authorized": true})
			})
			status := response.Code
			if err != nil {
				var apiError *router.ApiError
				if !errors.As(err, &apiError) {
					t.Fatal(err)
				}
				status = apiError.Status
			}
			if status != test.status {
				t.Fatalf("HTTP status = %d, want %d; body: %s", status, test.status, response.Body.String())
			}
		})
	}
}
