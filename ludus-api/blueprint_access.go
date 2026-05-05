package ludusapi

import (
	"slices"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"ludusapi/models"
)

// userCanAccessBlueprint reports whether user has read access to
// blueprintRecord. Admins always pass. Source-derived blueprints inherit
// access from the parent source's sharedUsers / sharedGroups.
func userCanAccessBlueprint(e *core.RequestEvent, user *models.User, blueprintRecord *core.Record) (bool, error) {
	if user.IsAdmin() {
		return true, nil
	}

	if blueprintRecord.GetString("owner") == user.Id {
		return true, nil
	}

	if slices.Contains(blueprintRecord.GetStringSlice("sharedUsers"), user.Id) {
		return true, nil
	}

	for _, groupID := range blueprintRecord.GetStringSlice("sharedGroups") {
		groupRecord, err := e.App.FindRecordById("groups", groupID)
		if err != nil {
			continue
		}
		if slices.Contains(groupRecord.GetStringSlice("members"), user.Id) ||
			slices.Contains(groupRecord.GetStringSlice("managers"), user.Id) {
			return true, nil
		}
	}

	if srcID := blueprintRecord.GetString("source"); srcID != "" {
		srcRecord, err := e.App.FindRecordById("sources", srcID)
		if err == nil && srcRecord != nil {
			if slices.Contains(srcRecord.GetStringSlice("sharedUsers"), user.Id) {
				return true, nil
			}
			for _, groupID := range srcRecord.GetStringSlice("sharedGroups") {
				groupRecord, gErr := e.App.FindRecordById("groups", groupID)
				if gErr != nil {
					continue
				}
				if slices.Contains(groupRecord.GetStringSlice("members"), user.Id) ||
					slices.Contains(groupRecord.GetStringSlice("managers"), user.Id) {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// userCanShareBlueprintWithUser: a non-admin can share a blueprint with
// another user only if they manage at least one group that includes the
// target.
func userCanShareBlueprintWithUser(e *core.RequestEvent, actingUser *models.User, targetUserRecord *core.Record) (bool, error) {
	if actingUser.IsAdmin() {
		return true, nil
	}

	groupRecords, err := e.App.FindRecordsByFilter(
		"groups",
		"managers.id ?= {:manager_id}",
		"-created",
		0,
		0,
		map[string]any{"manager_id": actingUser.Id},
	)
	if err != nil {
		return false, err
	}

	for _, groupRecord := range groupRecords {
		if slices.Contains(groupRecord.GetStringSlice("members"), targetUserRecord.Id) ||
			slices.Contains(groupRecord.GetStringSlice("managers"), targetUserRecord.Id) {
			return true, nil
		}
	}

	return false, nil
}

// getBlueprintAccessType labels how user gets access: admin / owner / direct
// (sharedUsers) / group (member of a sharedGroup) / source (inherited from
// the parent source) / unknown.
func getBlueprintAccessType(e *core.RequestEvent, user *models.User, blueprintRecord *core.Record) string {
	if user.IsAdmin() {
		return "admin"
	}

	if blueprintRecord.GetString("owner") == user.Id {
		return "owner"
	}

	if slices.Contains(blueprintRecord.GetStringSlice("sharedUsers"), user.Id) {
		return "direct"
	}

	for _, groupID := range blueprintRecord.GetStringSlice("sharedGroups") {
		groupRecord, err := e.App.FindRecordById("groups", groupID)
		if err != nil {
			continue
		}
		if slices.Contains(groupRecord.GetStringSlice("members"), user.Id) ||
			slices.Contains(groupRecord.GetStringSlice("managers"), user.Id) {
			return "group"
		}
	}

	if srcID := blueprintRecord.GetString("source"); srcID != "" {
		srcRecord, err := e.App.FindRecordById("sources", srcID)
		if err == nil && srcRecord != nil {
			if slices.Contains(srcRecord.GetStringSlice("sharedUsers"), user.Id) {
				return "source"
			}
			for _, groupID := range srcRecord.GetStringSlice("sharedGroups") {
				groupRecord, gErr := e.App.FindRecordById("groups", groupID)
				if gErr != nil {
					continue
				}
				if slices.Contains(groupRecord.GetStringSlice("members"), user.Id) ||
					slices.Contains(groupRecord.GetStringSlice("managers"), user.Id) {
					return "source"
				}
			}
		}
	}

	return "unknown"
}

// resolveUserIDs translates PocketBase user record IDs to user-facing
// userIDs. IDs that don't resolve are returned unchanged so callers can
// surface them.
func resolveUserIDs(e *core.RequestEvent, userRecordIDs []string) []string {
	userIDs := make([]string, 0, len(userRecordIDs))
	for _, userRecordID := range userRecordIDs {
		userID := userRecordID
		userRecord, err := e.App.FindRecordById("users", userRecordID)
		if err == nil {
			resolvedUserID := userRecord.GetString("userID")
			if resolvedUserID != "" {
				userID = resolvedUserID
			}
		}
		userIDs = append(userIDs, userID)
	}
	return userIDs
}

func resolveGroupNames(e *core.RequestEvent, groupRecordIDs []string) []string {
	groupNames := make([]string, 0, len(groupRecordIDs))
	for _, groupRecordID := range groupRecordIDs {
		groupName := groupRecordID
		groupRecord, err := e.App.FindRecordById("groups", groupRecordID)
		if err == nil {
			resolvedGroupName := groupRecord.GetString("name")
			if resolvedGroupName != "" {
				groupName = resolvedGroupName
			}
		}
		groupNames = append(groupNames, groupName)
	}
	return groupNames
}

func resolveOwnerUserID(e *core.RequestEvent, ownerRecordID string) string {
	ownerUserID := ownerRecordID
	ownerRecord, err := e.App.FindRecordById("users", ownerRecordID)
	if err == nil {
		resolvedOwnerUserID := ownerRecord.GetString("userID")
		if resolvedOwnerUserID != "" {
			ownerUserID = resolvedOwnerUserID
		}
	}
	return ownerUserID
}

func normalizeBulkIdentifiers(items []string) []string {
	normalized := make([]string, 0, len(items))
	seen := make(map[string]struct{})

	for _, item := range items {
		for _, part := range strings.Split(item, ",") {
			value := strings.TrimSpace(part)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			normalized = append(normalized, value)
		}
	}

	return normalized
}
