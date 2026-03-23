// Phase 2: Access control tests using PocketBase's in-memory test app.
// Ludus migrations run automatically, giving us real users/ranges/groups/blueprints
// collections without any external database or filesystem.
package ludusapi

import (
	"ludusapi/models"
	"os"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	// Ensure ludus migrations are registered via init()
	_ "ludusapi/migrations"
)

// --- Test helpers ---

// setupTestApp creates a fresh PocketBase test app with all ludus collections.
func setupTestApp(t *testing.T) *tests.TestApp {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "ludus_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	testApp, err := tests.NewTestApp(tempDir)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("failed to create test app: %v", err)
	}
	t.Cleanup(func() {
		testApp.Cleanup()
		os.RemoveAll(tempDir)
	})
	return testApp
}

// swapGlobalApp replaces the package-level `app` for functions that use it directly.
func swapGlobalApp(t *testing.T, testApp *tests.TestApp) {
	t.Helper()
	originalApp := app
	app = testApp
	t.Cleanup(func() { app = originalApp })
}

func makeRequestEvent(testApp *tests.TestApp) *core.RequestEvent {
	e := &core.RequestEvent{}
	e.App = testApp
	return e
}

func seedUser(t *testing.T, testApp *tests.TestApp, userID string, userNumber int, isAdmin bool) *core.Record {
	t.Helper()
	col, err := testApp.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection not found: %v", err)
	}
	rec := core.NewRecord(col)
	rec.Set("userID", userID)
	rec.Set("userNumber", userNumber)
	rec.Set("name", "Test User "+userID)
	rec.Set("isAdmin", isAdmin)
	rec.Set("email", userID+"@test.ludus.local")
	rec.Set("password", "testpassword123")
	if err := testApp.Save(rec); err != nil {
		t.Fatalf("failed to save user %s: %v", userID, err)
	}
	return rec
}

func seedRange(t *testing.T, testApp *tests.TestApp, rangeID string, rangeNumber int) *core.Record {
	t.Helper()
	col, err := testApp.FindCollectionByNameOrId("ranges")
	if err != nil {
		t.Fatalf("ranges collection not found: %v", err)
	}
	rec := core.NewRecord(col)
	rec.Set("rangeID", rangeID)
	rec.Set("rangeNumber", rangeNumber)
	rec.Set("name", "Test Range "+rangeID)
	rec.Set("rangeState", "NEVER DEPLOYED")
	if err := testApp.Save(rec); err != nil {
		t.Fatalf("failed to save range %s: %v", rangeID, err)
	}
	return rec
}

func seedGroup(t *testing.T, testApp *tests.TestApp, name string, memberIDs, managerIDs, rangeIDs []string) *core.Record {
	t.Helper()
	col, err := testApp.FindCollectionByNameOrId("groups")
	if err != nil {
		t.Fatalf("groups collection not found: %v", err)
	}
	rec := core.NewRecord(col)
	rec.Set("name", name)
	rec.Set("members", memberIDs)
	rec.Set("managers", managerIDs)
	rec.Set("ranges", rangeIDs)
	if err := testApp.Save(rec); err != nil {
		t.Fatalf("failed to save group %s: %v", name, err)
	}
	return rec
}

func seedBlueprint(t *testing.T, testApp *tests.TestApp, blueprintID string, ownerID string, sharedUserIDs, sharedGroupIDs []string) *core.Record {
	t.Helper()
	col, err := testApp.FindCollectionByNameOrId("blueprints")
	if err != nil {
		t.Fatalf("blueprints collection not found: %v", err)
	}
	rec := core.NewRecord(col)
	rec.Set("blueprintID", blueprintID)
	rec.Set("name", "Test Blueprint "+blueprintID)
	rec.Set("owner", ownerID)
	rec.Set("sharedUsers", sharedUserIDs)
	rec.Set("sharedGroups", sharedGroupIDs)
	// SaveNoValidate bypasses file field validation —
	// access control tests don't need an actual config file.
	if err := testApp.SaveNoValidate(rec); err != nil {
		t.Fatalf("failed to save blueprint %s: %v", blueprintID, err)
	}
	return rec
}

// --- HasRangeAccess tests ---

func TestHasRangeAccess_DirectAndGroupAccess(t *testing.T) {
	testApp := setupTestApp(t)
	swapGlobalApp(t, testApp)

	user1 := seedUser(t, testApp, "DIRECT1", 1, false)
	user2 := seedUser(t, testApp, "GRPMEMBER", 2, false)
	rangeRec := seedRange(t, testApp, "TESTRANGE", 10)

	// User 1 gets direct access
	user1.Set("ranges", []string{rangeRec.Id})
	testApp.Save(user1)

	// User 2 gets access via group
	seedGroup(t, testApp, "test-group", []string{user2.Id}, nil, []string{rangeRec.Id})

	e := makeRequestEvent(testApp)

	// Direct assignment works
	if !HasRangeAccess(e, "DIRECT1", 10) {
		t.Error("user with direct assignment should have access")
	}

	// Unassigned range denied
	if HasRangeAccess(e, "DIRECT1", 99) {
		t.Error("user should NOT have access to unassigned range")
	}

	// Group membership works (fresh request event — no cache)
	e2 := makeRequestEvent(testApp)
	if !HasRangeAccess(e2, "GRPMEMBER", 10) {
		t.Error("user with group membership should have access")
	}
}

// --- userCanAccessBlueprint tests ---

func TestUserCanAccessBlueprint_OwnerAndSharedGroup(t *testing.T) {
	testApp := setupTestApp(t)

	ownerRec := seedUser(t, testApp, "OWNER", 10, false)
	memberRec := seedUser(t, testApp, "MEMBER", 11, false)
	strangerRec := seedUser(t, testApp, "STRANGER", 12, false)

	groupRec := seedGroup(t, testApp, "bp-group", []string{memberRec.Id}, nil, nil)
	bpRec := seedBlueprint(t, testApp, "test-bp", ownerRec.Id, nil, []string{groupRec.Id})

	e := makeRequestEvent(testApp)

	// Owner has access
	owner := &models.User{}
	owner.SetProxyRecord(ownerRec)
	if ok, err := userCanAccessBlueprint(e, owner, bpRec); err != nil || !ok {
		t.Errorf("owner should have access, got ok=%v err=%v", ok, err)
	}

	// Group member has access
	member := &models.User{}
	member.SetProxyRecord(memberRec)
	if ok, err := userCanAccessBlueprint(e, member, bpRec); err != nil || !ok {
		t.Errorf("group member should have access, got ok=%v err=%v", ok, err)
	}

	// Stranger denied
	stranger := &models.User{}
	stranger.SetProxyRecord(strangerRec)
	if ok, err := userCanAccessBlueprint(e, stranger, bpRec); err != nil || ok {
		t.Errorf("stranger should be denied, got ok=%v err=%v", ok, err)
	}
}
