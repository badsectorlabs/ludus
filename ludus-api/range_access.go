package ludusapi

import (
	"fmt"
	"ludusapi/dto"
	"ludusapi/models"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// This will run just the access-control tag on the provided range
func RunAccessControlPlaybook(e *core.RequestEvent, targetRange *models.Range) error {

	// Since RunPlaybookWithTag will use the range in the context, we need to set it to the target range, and then restore it after we run the playbook
	originalRange := e.Get("range").(*models.Range)
	defer func() {
		e.Set("range", originalRange)
	}()

	e.Set("range", targetRange)

	_, err := RunPlaybookWithTag(e, "range-access.yml", "", false)
	if err != nil {
		return err
	}

	return nil
}

// GetRangeAccessibleUsers returns all userIDs who can access a specific range
func GetRangeAccessibleUsers(rangeNumber int) []dto.ListRangeUsersResponseItem {
	var result []dto.ListRangeUsersResponseItem

	rangeRecord, err := GetRangeObjectByNumber(rangeNumber)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding range: %s", err.Error()))
		return nil
	}

	// Find all users who have direct access to the range by querying the user table looking for the range.Id in the user's ranges array
	userRecords, err := app.FindRecordsByFilter(
		"users",                    // collection name
		"ranges.id ?= {:range_id}", // filter
		"-created",                 // sort
		0,                          // limit
		0,                          // offset
		dbx.Params{
			"range_id": rangeRecord.Id,
		},
	)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding users: %s", err.Error()))
		return nil
	}
	for _, userRecord := range userRecords {
		result = append(result, dto.ListRangeUsersResponseItem{
			UserID:     userRecord.GetString("userID"),
			Name:       userRecord.GetString("name"),
			AccessType: "Direct",
		})
	}

	// Find all users who are managers or members of a group with access to the range by querying the group table looking for the range.Id in the group's ranges array
	groupRecords, err := app.FindRecordsByFilter(
		"groups",                   // collection name
		"ranges.id ?= {:range_id}", // filter
		"-created",                 // sort
		0,                          // limit
		0,                          // offset
		dbx.Params{
			"range_id": rangeRecord.Id,
		},
	)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding groups: %s", err.Error()))
		return nil
	}
	for _, groupRecord := range groupRecords {
		app.ExpandRecord(groupRecord, []string{"members", "managers"}, nil)
		for _, member := range groupRecord.ExpandedAll("members") {
			result = append(result, dto.ListRangeUsersResponseItem{
				UserID:     member.GetString("userID"),
				Name:       member.GetString("name"),
				AccessType: "Group Member",
			})
		}
		for _, manager := range groupRecord.ExpandedAll("managers") {
			result = append(result, dto.ListRangeUsersResponseItem{
				UserID:     manager.GetString("userID"),
				Name:       manager.GetString("name"),
				AccessType: "Group Manager",
			})
		}
	}

	// Sort the result to ensure consistent ordering
	slices.SortFunc(result, func(a, b dto.ListRangeUsersResponseItem) int {
		return strings.Compare(a.UserID, b.UserID)
	})

	return result
}

func GetAccessibleRangesForUser(user *models.User) ([]dto.ListUserAccessibleRangesResponseItem, error) {
	var result []dto.ListUserAccessibleRangesResponseItem
	var err error

	// Get direct range assignments
	userRanges := user.Ranges()
	for _, rangeRecord := range userRanges {
		result = append(result, dto.ListUserAccessibleRangesResponseItem{
			RangeNumber: rangeRecord.GetInt("rangeNumber"),
			RangeID:     rangeRecord.RangeId(),
			AccessType:  "Direct",
		})
	}

	// Find all groups the user is a member of or manager of
	groupRecords, err := app.FindRecordsByFilter(
		"groups", // collection name
		"members.id ?= {:user_id} || managers.id ?= {:user_id}", // filter
		"-created", // sort
		0,          // limit
		0,          // offset
		dbx.Params{
			"user_id": user.Id,
		},
	)

	if err != nil {
		logger.Error(fmt.Sprintf("Error finding groups: %s", err.Error()))
		return nil, fmt.Errorf("error finding groups: %w", err)
	}

	for _, groupRecord := range groupRecords {
		app.ExpandRecord(groupRecord, []string{"ranges"}, nil)
		for _, rangeRecord := range groupRecord.ExpandedAll("ranges") {
			result = append(result, dto.ListUserAccessibleRangesResponseItem{
				RangeNumber: rangeRecord.GetInt("rangeNumber"),
				RangeID:     rangeRecord.GetString("rangeID"),
				AccessType:  "Group",
			})
		}
	}

	// Sort the result to ensure consistent ordering
	slices.SortFunc(result, func(a, b dto.ListUserAccessibleRangesResponseItem) int {
		return int(a.RangeNumber - b.RangeNumber)
	})

	return result, nil
}

func rangeIsDeployed(e *core.RequestEvent, rangeRecord *models.Range) (bool, error) {
	// We only care about the error if the range is deployed
	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		logger.Error(fmt.Sprintf("Error getting proxmox client for user %s: %v", e.Get("user").(*models.User).UserId(), err))
		return false, fmt.Errorf("error getting proxmox client for user %s: %w", e.Get("user").(*models.User).UserId(), err)
	}
	updateRangeVMData(e, rangeRecord, proxmoxClient)
	// Validate the range is deployed and if so, return an error, otherwise the access will be handled correctly on next deploy.
	if rangeRecord.NumberOfVms() > 0 {
		return true, nil
	}
	return false, nil
}
