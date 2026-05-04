package ludusapi

import (
	"database/sql"
	stderrors "errors"
	"fmt"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"slices"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func GetGroupObjectFromRequest(e *core.RequestEvent) (*models.Group, error) {
	groupName := e.Request.PathValue("groupName")

	if groupName == "" {
		return nil, JSONError(e, http.StatusBadRequest, "Group name is required and not found in the request path")
	}

	groupRecord, err := e.App.FindFirstRecordByData("groups", "name", groupName)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, JSONError(e, http.StatusNotFound, fmt.Sprintf("Group %s not found", groupName))
		}
		return nil, JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding group: %v", err))
	}
	group := &models.Group{}
	group.SetProxyRecord(groupRecord)
	e.App.ExpandRecord(group.Record, []string{"members", "managers", "ranges"}, nil)
	return group, nil
}

// CreateGroup creates a new group (admin only)
func CreateGroup(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot create groups")
	}

	var payload dto.CreateGroupRequest
	if err := e.BindBody(&payload); err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	if payload.Name == "" {
		return JSONError(e, http.StatusBadRequest, "Group name is required")
	}

	// Check if group with this name already exists
	existingGroupRecord, err := e.App.FindFirstRecordByData("groups", "name", payload.Name)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding group: %v", err))
	}
	if existingGroupRecord != nil {
		return JSONError(e, http.StatusConflict, "Group with this name already exists")
	}

	groupCollection, err := e.App.FindCollectionByNameOrId("groups")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding group collection: %v", err))
	}
	groupRecord := core.NewRecord(groupCollection)
	group := &models.Groups{}
	group.SetProxyRecord(groupRecord)
	group.SetName(payload.Name)
	group.SetDescription(payload.Description)

	if err := e.App.Save(group); err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error creating group: %v", err))
	}

	// Create the group in proxmox
	err = createGroupInProxmox(group.Name())
	if err != nil {
		e.App.Delete(group)
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error creating group in proxmox: %v", err))
	}

	return JSONResult(e, http.StatusCreated, "Group created successfully")
}

// DeleteGroup deletes a group and cleans up memberships (admin only)
func DeleteGroup(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot delete groups")
	}

	group, err := GetGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	// Delete the group from proxmox
	err = removeGroupFromProxmox(group.Name())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error deleting group from proxmox: %v", err))
	}

	// Delete the group
	if err := e.App.Delete(group); err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error deleting group: %v", err))
	}

	return JSONResult(e, http.StatusOK, "Group deleted successfully")
}

// ListGroups lists all groups (admin only)
func ListGroups(e *core.RequestEvent) error {
	var groups []dto.ListGroupsResponseItem
	user := e.Get("user").(*models.User)

	if e.Auth.GetBool("isAdmin") {
		groupsRecords, err := e.App.FindAllRecords("groups")
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error listing groups: %v", err))
		}
		for _, groupRecord := range groupsRecords {
			group := &models.Group{}
			group.SetProxyRecord(groupRecord)
			e.App.ExpandRecord(groupRecord, []string{"members", "managers", "ranges"}, nil)
			groupMembers := group.Members()
			groupManagers := group.Managers()
			groupRanges := group.Ranges()
			groupItem := dto.ListGroupsResponseItem{
				Name:        group.Name(),
				Description: group.Description(),
				NumMembers:  len(groupMembers),
				NumManagers: len(groupManagers),
				NumRanges:   len(groupRanges),
			}
			groups = append(groups, groupItem)
		}
	} else {
		groupsRecords := user.Groups()
		for _, group := range groupsRecords {
			e.App.ExpandRecord(group.Record, []string{"members", "managers", "ranges"}, nil)
			groupMembers := group.Members()
			groupManagers := group.Managers()
			groupRanges := group.Ranges()
			groupItem := dto.ListGroupsResponseItem{
				Name:        group.Name(),
				Description: group.Description(),
				NumMembers:  len(groupMembers),
				NumManagers: len(groupManagers),
				NumRanges:   len(groupRanges),
			}
			groups = append(groups, groupItem)
		}
	}
	return e.JSON(http.StatusOK, groups)
}

// AddUsersToGroup adds one or more users to a group
func AddUsersToGroup(e *core.RequestEvent) error {

	group, err := GetGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	if !userIsManagerOfGroup(actingUser, group) && !actingUser.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "You are not a manager of this group and cannot add users to it")
	}

	var bulkRequest dto.BulkAddUsersToGroupRequest
	if err := e.BindBody(&bulkRequest); err != nil || len(bulkRequest.UserIDs) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}
	userIDs := bulkRequest.UserIDs
	managers := bulkRequest.Managers

	// Process all userIDs
	var success []string
	var errors []dto.BulkGroupOperationErrorItem

	for _, userID := range userIDs {
		isManager := slices.Contains(managers, userID)

		// Check if user exists
		var user models.User
		userRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error finding user: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("User %s not found", userID),
			})
			continue
		}
		user.SetProxyRecord(userRecord)

		// Check if user is already a member
		e.App.ExpandRecord(userRecord, []string{"groups"}, nil)
		userGroups := user.Groups()
		alreadyMember := false
		for _, userGroup := range userGroups {
			if userGroup.Id == group.Id {
				alreadyMember = true
				break
			}
		}
		if alreadyMember {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("User %s is already a member of group %s", userID, group.Name()),
			})
			continue
		}

		// Add user to group
		user.Set("groups+", group.Id)
		if isManager {
			group.Set("managers+", user.Id)
		} else {
			group.Set("members+", user.Id)
		}

		// Add user to group in proxmox
		err = addUserToGroupInProxmox(user.ProxmoxUsername(), user.ProxmoxRealm(), group.Name())
		if err != nil {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error adding user to proxmox: %v", err),
			})
			// Rollback
			user.Set("groups-", group.Id)
			if isManager {
				group.Set("managers-", user.Id)
			} else {
				group.Set("members-", user.Id)
			}
			continue
		}

		e.App.Save(user)
		e.App.Save(group)

		// For each range in the group, run the access control playbook
		playbookError := false
		for _, rangeRecord := range group.Ranges() {
			err = RunAccessControlPlaybook(e, rangeRecord)
			if err != nil {
				errorString := ""
				if stderrors.Is(err, ErrRangeRouterPoweredOff) {
					errorString = err.Error()
				} else {
					isDeployed, deployErr := rangeIsDeployed(e, rangeRecord)
					if deployErr != nil {
						errorString = fmt.Sprintf("Error checking if range %s is deployed: %v", rangeRecord.RangeId(), deployErr)
					} else if isDeployed {
						errorString = fmt.Sprintf("Range %s is deployed and access cannot be added to group %s", rangeRecord.RangeId(), group.Name())
					}
				}
				if errorString != "" {
					// Rollback
					user.Set("groups-", group.Id)
					e.App.Save(user)
					if isManager {
						group.Set("managers-", user.Id)
					} else {
						group.Set("members-", user.Id)
					}
					e.App.Save(group)
					errors = append(errors, dto.BulkGroupOperationErrorItem{
						Item:   userID,
						Reason: errorString,
					})
					playbookError = true
					break
				}
			}
		}
		if !playbookError {
			success = append(success, userID)
		}
	}

	return e.JSON(http.StatusOK, dto.BulkGroupOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

// RemoveUsersFromGroup removes one or more users from a group
func RemoveUsersFromGroup(e *core.RequestEvent) error {

	group, err := GetGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	if !userIsManagerOfGroup(actingUser, group) && !actingUser.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "You are not a manager of this group and cannot remove users from it")
	}

	var bulkRequest dto.BulkRemoveUsersFromGroupRequest
	if err := e.BindBody(&bulkRequest); err != nil || len(bulkRequest.UserIDs) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}
	userIDs := bulkRequest.UserIDs

	// Process all userIDs
	var success []string
	var errors []dto.BulkGroupOperationErrorItem

	for _, userID := range userIDs {
		// Check if user exists
		var user models.User
		userRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error finding user: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("User %s not found", userID),
			})
			continue
		}
		user.SetProxyRecord(userRecord)

		// Remove user from group in proxmox
		err = removeUserFromGroupInProxmox(user.ProxmoxUsername(), user.ProxmoxRealm(), group.Name())
		if err != nil {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error removing user from proxmox: %v", err),
			})
			continue
		}

		// Remove user from group
		user.Set("groups-", group.Id)
		// Remove user from managers and members of the group (could be either)
		isManager := userIsManagerOfGroup(&user, group)
		if isManager {
			group.Set("managers-", user.Id)
		} else {
			group.Set("members-", user.Id)
		}
		e.App.Save(user)
		e.App.Save(group)

		// For each range in the group, run the access control playbook
		playbookError := false
		for _, rangeRecord := range group.Ranges() {
			// If there is an error here, potentially a user could still have access to a range via WireGuard.
			err = RunAccessControlPlaybook(e, rangeRecord)
			if err != nil {
				errorString := ""
				if stderrors.Is(err, ErrRangeRouterPoweredOff) {
					errorString = err.Error()
				} else {
					isDeployed, deployErr := rangeIsDeployed(e, rangeRecord)
					if deployErr != nil {
						errorString = fmt.Sprintf("Error checking if range %s is deployed: %v", rangeRecord.RangeId(), deployErr)
					} else if isDeployed {
						errorString = fmt.Sprintf("Range %s is deployed and access cannot be removed from group %s", rangeRecord.RangeId(), group.Name())
					}
				}
				if errorString != "" {
					// If there is an error or the range is deployed, we need to restore the user to the group and range
					if isManager {
						group.Set("managers+", user.Id)
					} else {
						group.Set("members+", user.Id)
					}
					e.App.Save(group)
					user.Set("groups+", group.Id)
					e.App.Save(user)
					errors = append(errors, dto.BulkGroupOperationErrorItem{
						Item:   userID,
						Reason: errorString,
					})
					playbookError = true
					break
				}
			}
		}
		if !playbookError {
			success = append(success, userID)
		}
	}

	return e.JSON(http.StatusOK, dto.BulkGroupOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

// AddRangesToGroup grants group access to one or more ranges
func AddRangesToGroup(e *core.RequestEvent) error {

	group, err := GetGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	if !userIsManagerOfGroup(user, group) && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You are not a manager of group %s or an admin and cannot add ranges to it", group.Name()))
	}

	var bulkRequest dto.BulkAddRangesToGroupRequest
	if err := e.BindBody(&bulkRequest); err != nil || len(bulkRequest.RangeIDs) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with rangeIDs is required")
	}
	rangeIDs := bulkRequest.RangeIDs

	// Process all rangeIDs
	var success []string
	var errors []dto.BulkGroupOperationErrorItem

	for _, rangeID := range rangeIDs {
		// Check if range exists
		rangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeID", rangeID)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   rangeID,
				Reason: fmt.Sprintf("Error finding range: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   rangeID,
				Reason: fmt.Sprintf("Range %s not found", rangeID),
			})
			continue
		}
		rangeObj := &models.Range{}
		rangeObj.SetProxyRecord(rangeRecord)

		// Check if group already has access to this range
		e.App.ExpandRecord(group.Record, []string{"ranges"}, nil)
		groupRanges := group.Ranges()
		alreadyHasAccess := false
		for _, groupRange := range groupRanges {
			if groupRange.Id == rangeObj.Id {
				alreadyHasAccess = true
				break
			}
		}
		if alreadyHasAccess {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   rangeID,
				Reason: fmt.Sprintf("Group %s already has access to range %s", group.Name(), rangeObj.RangeId()),
			})
			continue
		}

		// Check if the acting user has access to the range they want to add to the group
		if !HasRangeAccess(e, user.UserId(), rangeObj.RangeNumber()) && !user.IsAdmin() {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   rangeID,
				Reason: fmt.Sprintf("You do not have access to range %s and cannot add it to group %s", rangeObj.RangeId(), group.Name()),
			})
			continue
		}

		// Grant group access to range in proxmox
		err = grantGroupAccessToRangeInProxmox(group.Name(), rangeObj.RangeId(), rangeObj.RangeNumber())
		if err != nil {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   rangeID,
				Reason: fmt.Sprintf("Error granting group access to proxmox: %v", err),
			})
			continue
		}

		group.Set("ranges+", rangeObj.Id)
		e.App.Save(group)

		err = RunAccessControlPlaybook(e, rangeObj)
		if err != nil {
			errorString := ""
			if stderrors.Is(err, ErrRangeRouterPoweredOff) {
				errorString = err.Error()
			} else {
				isDeployed, deployErr := rangeIsDeployed(e, rangeObj)
				if deployErr != nil {
					errorString = fmt.Sprintf("Error checking if range %s is deployed: %v", rangeObj.RangeId(), deployErr)
				} else if isDeployed {
					errorString = fmt.Sprintf("Range %s is deployed and access cannot be granted to group %s. Make sure the router is powered on and accessible.", rangeObj.RangeId(), group.Name())
				}
			}
			if errorString != "" {
				// Rollback
				group.Set("ranges-", rangeObj.Id)
				e.App.Save(group)
				errors = append(errors, dto.BulkGroupOperationErrorItem{
					Item:   rangeID,
					Reason: errorString,
				})
				continue
			}
		}

		success = append(success, rangeID)
	}

	return e.JSON(http.StatusOK, dto.BulkGroupOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

// RemoveRangesFromGroup revokes group access from one or more ranges
func RemoveRangesFromGroup(e *core.RequestEvent) error {

	group, err := GetGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	if !userIsManagerOfGroup(user, group) && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You are not a manager of group %s or an admin and cannot remove ranges from it", group.Name()))
	}

	var bulkRequest dto.BulkRemoveRangesFromGroupRequest
	if err := e.BindBody(&bulkRequest); err != nil || len(bulkRequest.RangeIDs) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with rangeIDs is required")
	}
	rangeIDs := bulkRequest.RangeIDs

	// Process all rangeIDs
	var success []string
	var errors []dto.BulkGroupOperationErrorItem

	for _, rangeID := range rangeIDs {
		rangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeID", rangeID)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   rangeID,
				Reason: fmt.Sprintf("Error finding range: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   rangeID,
				Reason: fmt.Sprintf("Range %s not found", rangeID),
			})
			continue
		}
		rangeObj := &models.Range{}
		rangeObj.SetProxyRecord(rangeRecord)

		// Remove group access to range in proxmox
		err = revokeGroupAccessToRangeInProxmox(group.Name(), rangeObj.RangeId(), rangeObj.RangeNumber())
		if err != nil {
			errors = append(errors, dto.BulkGroupOperationErrorItem{
				Item:   rangeID,
				Reason: fmt.Sprintf("Error revoking group access from proxmox: %v", err),
			})
			continue
		}

		// Remove group access to range
		group.Set("ranges-", rangeObj.Id)
		e.App.Save(group)

		err = RunAccessControlPlaybook(e, rangeObj)
		if err != nil {
			errorString := ""
			if stderrors.Is(err, ErrRangeRouterPoweredOff) {
				errorString = err.Error()
			} else {
				isDeployed, deployErr := rangeIsDeployed(e, rangeObj)
				if deployErr != nil {
					errorString = fmt.Sprintf("Error checking if range %s is deployed: %v", rangeObj.RangeId(), deployErr)
				} else if isDeployed {
					errorString = fmt.Sprintf("Range %s is deployed and access cannot be revoked from group %s", rangeObj.RangeId(), group.Name())
				}
			}
			if errorString != "" {
				// Rollback
				group.Set("ranges+", rangeObj.Id)
				e.App.Save(group)
				errors = append(errors, dto.BulkGroupOperationErrorItem{
					Item:   rangeID,
					Reason: errorString,
				})
				continue
			}
		}

		success = append(success, rangeID)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"result": dto.BulkGroupOperationResponse{
			Success: success,
			Errors:  errors,
		},
	})
}

// ListGroupMembers lists users in a group (admin only)
func ListGroupMembers(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot list group members")
	}

	group, err := GetGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	// Get group members
	e.App.ExpandRecord(group.Record, []string{"members", "managers"}, nil)
	groupMembers := group.Members()
	groupManagers := group.Managers()

	var membersAndManagersArray []dto.ListGroupMembersResponseItem
	for _, user := range groupMembers {
		membersAndManagersArray = append(membersAndManagersArray, dto.ListGroupMembersResponseItem{
			UserID: user.UserId(),
			Name:   user.Name(),
			Role:   "member",
		})
	}
	for _, user := range groupManagers {
		membersAndManagersArray = append(membersAndManagersArray, dto.ListGroupMembersResponseItem{
			UserID: user.UserId(),
			Name:   user.Name(),
			Role:   "manager",
		})
	}
	return e.JSON(http.StatusOK, membersAndManagersArray)
}

// ListGroupRanges lists ranges accessible to a group (admin only)
func ListGroupRanges(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot list group ranges")
	}

	group, err := GetGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	e.App.ExpandRecord(group.Record, []string{"ranges"}, nil)
	groupRanges := group.Ranges()
	var ranges []dto.ListGroupRangesResponseItem
	for _, rangeRecord := range groupRanges {
		e.App.ExpandRecord(rangeRecord.Record, []string{"VMs"}, nil)
		vms, err := getVMsForRange(rangeRecord.RangeId())
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting VMs for range: %v", err))
		}
		var rangeVMs []dto.ListGroupRangesResponseItemVMsItem
		for _, vm := range vms {
			rangeVMs = append(rangeVMs, dto.ListGroupRangesResponseItemVMsItem{
				RangeNumber: int32(rangeRecord.RangeNumber()),
				Name:        vm.Name(),
				PoweredOn:   vm.PoweredOn(),
				Ip:          vm.Ip(),
				IsRouter:    vm.IsRouter(),
				ID:          int32(vm.ProxmoxId()),
				ProxmoxID:   int32(vm.ProxmoxId()),
				CPU:         int32(vm.Cpu()),
				RAM:         int32(vm.Ram()),
			})
		}
		ranges = append(ranges, dto.ListGroupRangesResponseItem{
			NumberOfVMs:    int32(rangeRecord.NumberOfVms()),
			AllowedIPs:     rangeRecord.AllowedIps(),
			AllowedDomains: rangeRecord.AllowedDomains(),
			RangeState:     rangeRecord.RangeState(),
			RangeNumber:    int32(rangeRecord.RangeNumber()),
			Description:    rangeRecord.Description(),
			Purpose:        rangeRecord.Purpose(),
			ThumbnailUrl:   rangeThumbnailURL(rangeRecord),
			LastDeployment: rangeRecord.LastDeployment().Time(),
			TestingEnabled: rangeRecord.TestingEnabled(),
			VMs:            rangeVMs,
			RangeID:        rangeRecord.RangeId(),
			Name:           rangeRecord.Name(),
		})
	}
	return e.JSON(http.StatusOK, ranges)

}

// GetUserMemberships returns the groups a user belongs to, along with their role in each group.
// By default, it returns memberships for the authenticated user.
// Admins may impersonate another user by providing the `userID` query parameter.
func GetUserMemberships(e *core.RequestEvent) error {
	targetUser := e.Get("user").(*models.User)

	// Find all groups where the target user is either a member or a manager
	groupRecords, err := e.App.FindRecordsByFilter(
		"groups",
		"members.id ?= {:user_id} || managers.id ?= {:user_id}",
		"-created",
		0,
		0,
		dbx.Params{
			"user_id": targetUser.Id,
		},
	)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error listing groups for user: %v", err))
	}

	memberships := make([]dto.GetUserMembershipsResponseItem, 0, len(groupRecords))

	for _, groupRecord := range groupRecords {
		group := &models.Group{}
		group.SetProxyRecord(groupRecord)

		// Expand relations so we can compute role and counts
		e.App.ExpandRecord(groupRecord, []string{"members", "managers", "ranges"}, nil)

		role := "member"
		if userIsManagerOfGroup(targetUser, group) {
			role = "manager"
		}

		memberships = append(memberships, dto.GetUserMembershipsResponseItem{
			GroupName:   group.Name(),
			Description: group.Description(),
			Role:        role,
		})
	}

	return e.JSON(http.StatusOK, memberships)
}
