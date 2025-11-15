package ludusapi

import (
	"database/sql"
	"fmt"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

func getGroupObjectFromRequest(e *core.RequestEvent) (*models.Group, error) {
	groupName := e.Request.PathValue("groupName")

	if groupName == "" {
		return nil, JSONError(e, http.StatusBadRequest, "Group name is required and not found in the request path")
	}

	groupRecord, err := e.App.FindFirstRecordByData("groups", "name", groupName)
	if err != nil {
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

	group, err := getGroupObjectFromRequest(e)
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

// AddUserToGroup adds a user to a group (admin only)
func AddUserToGroup(e *core.RequestEvent) error {

	group, err := getGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	if !userIsManagerOfGroup(actingUser, group) && !actingUser.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "You are not a manager of this group and cannot add users to it")
	}

	userID := e.Request.PathValue("userID")
	if userID == "" {
		return JSONError(e, http.StatusBadRequest, "User ID is required")
	}

	// Check if user exists
	var user models.User
	userRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding user: %v", err))
	}
	if err == sql.ErrNoRows {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("User %s not found", userID))
	}
	user.SetProxyRecord(userRecord)

	// Check if user is already a member
	e.App.ExpandRecord(userRecord, []string{"groups"}, nil)
	userGroups := user.Groups()
	for _, userGroup := range userGroups {
		if userGroup.Id == group.Id {
			return JSONError(e, http.StatusConflict, fmt.Sprintf("User %s is already a member of group %s", userID, group.Name()))
		}
	}

	// Add user to group
	user.Set("groups+", group.Id)

	// Check if the user is to be a manager of the group
	if e.Request.URL.Query().Get("manager") == "true" {
		group.Set("managers+", user.Id)
	} else {
		group.Set("members+", user.Id)
	}

	e.App.Save(user)
	e.App.Save(group)

	// Add user to group in proxmox
	err = addUserToGroupInProxmox(user.ProxmoxUsername(), user.ProxmoxRealm(), group.Name())
	if err != nil {
		user.Set("groups-", group.Id)
		if e.Request.URL.Query().Get("manager") == "true" {
			group.Set("managers-", user.Id)
		} else {
			group.Set("members-", user.Id)
		}
		e.App.Save(user)
		e.App.Save(group)
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error adding user %s to group %s in proxmox: %v", userID, group.Name(), err))
	}

	return JSONResult(e, http.StatusCreated, "User added to group successfully")
}

// RemoveUserFromGroup removes a user from a group (admin only)
func RemoveUserFromGroup(e *core.RequestEvent) error {

	group, err := getGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	if !userIsManagerOfGroup(actingUser, group) && !actingUser.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "You are not a manager of this group and cannot remove users from it")
	}

	userID := e.Request.PathValue("userID")
	if userID == "" {
		return JSONError(e, http.StatusBadRequest, "User ID is required")
	}

	// Check if user exists
	var user models.User
	userRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding user: %v", err))
	}
	if err == sql.ErrNoRows {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("User %s not found", userID))
	}
	user.SetProxyRecord(userRecord)

	// Remove user from group in proxmox
	err = removeUserFromGroupInProxmox(user.ProxmoxUsername(), user.ProxmoxRealm(), group.Name())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error removing user %s from group %s in proxmox: %v", userID, group.Name(), err))
	}

	// Remove user from group
	user.Set("groups-", group.Id)
	// Remove user from managers and members of the group (could be either)
	group.Set("managers-", user.Id)
	group.Set("members-", user.Id)
	e.App.Save(user)
	e.App.Save(group)

	return JSONResult(e, http.StatusOK, "User removed from group successfully")
}

// AddRangeToGroup grants group access to a range (admin only)
func AddRangeToGroup(e *core.RequestEvent) error {

	group, err := getGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	if !userIsManagerOfGroup(user, group) && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You are not a manager of group %s or an admin and cannot add ranges to it", group.Name()))
	}

	rangeID := e.Request.PathValue("rangeID")
	if rangeID == "" {
		return JSONError(e, http.StatusBadRequest, "Range ID is required")
	}

	// Check if range exists
	rangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding range: %v", err))
	}
	if err == sql.ErrNoRows {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found", rangeID))
	}
	rangeObj := &models.Range{}
	rangeObj.SetProxyRecord(rangeRecord)

	// Check if group already has access to this range
	e.App.ExpandRecord(group.Record, []string{"ranges"}, nil)
	groupRanges := group.Ranges()
	for _, groupRange := range groupRanges {
		if groupRange.Id == rangeObj.Id {
			return JSONError(e, http.StatusConflict, fmt.Sprintf("Group %s already has access to range %s", group.Name(), rangeObj.RangeId()))
		}
	}

	// Check if the acting user has access to the range they want to add to the group
	if !HasRangeAccess(user.UserId(), rangeObj.RangeNumber()) && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You do not have access to range %s and cannot add it to group %s", rangeObj.RangeId(), group.Name()))
	}

	group.Set("ranges+", rangeObj.Id)

	// Grant group access to range in proxmox
	err = grantGroupAccessToRangeInProxmox(group.Name(), rangeObj.RangeId())
	if err != nil {
		group.Set("ranges-", rangeObj.Id)
		e.App.Save(group)
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error granting group %s access to range %s in proxmox: %v", group.Name(), rangeObj.RangeId(), err))
	}

	e.App.Save(group)

	return JSONResult(e, http.StatusCreated, fmt.Sprintf("Group %s access to range %s granted successfully", group.Name(), rangeObj.RangeId()))
}

// RemoveRangeFromGroup revokes group access from a range (admin only)
func RemoveRangeFromGroup(e *core.RequestEvent) error {

	group, err := getGroupObjectFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	if !userIsManagerOfGroup(user, group) && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You are not a manager of group %s or an admin and cannot remove ranges from it", group.Name()))
	}

	rangeID := e.Request.PathValue("rangeID")
	if rangeID == "" {
		return JSONError(e, http.StatusBadRequest, "Range ID is required")
	}

	rangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding range: %v", err))
	}
	if err == sql.ErrNoRows {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found", rangeID))
	}
	rangeObj := &models.Range{}
	rangeObj.SetProxyRecord(rangeRecord)

	// Remove group access to range in proxmox
	err = revokeGroupAccessToRangeInProxmox(group.Name(), rangeObj.RangeId())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error revoking group %s access to range %s in proxmox: %v", group.Name(), rangeObj.RangeId(), err))
	}

	// Remove group access to range
	group.Set("ranges-", rangeObj.Id)
	e.App.Save(group)

	return JSONResult(e, http.StatusOK, fmt.Sprintf("Group %s access to range %s revoked successfully", group.Name(), rangeObj.RangeId()))
}

// ListGroupMembers lists users in a group (admin only)
func ListGroupMembers(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot list group members")
	}

	group, err := getGroupObjectFromRequest(e)
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
	response := dto.ListGroupMembersResponse{
		Result: membersAndManagersArray,
	}
	return e.JSON(http.StatusOK, response)
}

// ListGroupRanges lists ranges accessible to a group (admin only)
func ListGroupRanges(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot list group ranges")
	}

	group, err := getGroupObjectFromRequest(e)
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
			LastDeployment: rangeRecord.LastDeployment().Time(),
			TestingEnabled: rangeRecord.TestingEnabled(),
			VMs:            rangeVMs,
			RangeID:        rangeRecord.RangeId(),
			Name:           rangeRecord.Name(),
		})
	}
	response := dto.ListGroupRangesResponse{
		Result: ranges,
	}
	return e.JSON(http.StatusOK, response)

}
