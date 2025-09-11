package ludusapi

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func getGroupObjectFromRequest(c *gin.Context) (GroupObject, error) {
	groupName := c.Param("groupName")

	var group GroupObject
	if err := db.First(&group, "name = ?", groupName).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding group: %v", err)})
		}
		return group, err
	}
	return group, nil
}

// CreateGroup creates a new group (admin only)
func CreateGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	var group GroupObject
	if err := c.Bind(&group); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if group.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Group name is required"})
		return
	}

	// Check if group with this name already exists
	var existingGroup GroupObject
	if err := db.Where("name = ?", group.Name).First(&existingGroup).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Group with this name already exists"})
		return
	}

	if err := db.Create(&group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating group: %v", err)})
		return
	}

	// Create the group in proxmox
	err := createGroupInProxmox(group.Name)
	if err != nil {
		db.Delete(&group)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating group in proxmox: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"result": group})
}

// DeleteGroup deletes a group and cleans up memberships (admin only)
func DeleteGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	group, err := getGroupObjectFromRequest(c)
	if err != nil {
		return
	}

	// Delete the group from proxmox
	err = removeGroupFromProxmox(group.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error deleting group from proxmox: %v", err)})
		return
	}

	// Clean up all memberships and range access for this group
	db.Where("group_id = ?", group.ID).Delete(&UserGroupMembership{})
	db.Where("group_id = ?", group.ID).Delete(&GroupRangeAccess{})

	// Delete the group
	if err := db.Delete(&group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error deleting group: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "Group deleted successfully"})
}

// ListGroups lists all groups (admin only)
func ListGroups(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	var groups []GroupObject
	if err := db.Find(&groups).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error listing groups: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": groups})
}

// AddUserToGroup adds a user to a group (admin only)
func AddUserToGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	group, err := getGroupObjectFromRequest(c)
	if err != nil {
		return
	}

	userID := c.Param("userID")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User ID is required"})
		return
	}

	// Check if user exists
	var user UserObject
	if err := db.First(&user, "user_id = ?", userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("User %s not found", userID)})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding user %s: %v", userID, err)})
		}
		return
	}

	// Check if user is already a member
	var existingMembership UserGroupMembership
	if err := db.Where("user_id = ? AND group_id = ?", userID, group.ID).First(&existingMembership).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("User %s is already a member of group %s", userID, group.Name)})
		return
	}

	// Add user to group
	membership := UserGroupMembership{
		UserID:  userID,
		GroupID: uint(group.ID),
	}

	if err := db.Create(&membership).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error adding user %s to group %s: %v", userID, group.Name, err)})
		return
	}

	// Add user to group in proxmox
	err = addUserToGroupInProxmox(user.ProxmoxUsername, "pam", group.Name)
	if err != nil {
		db.Delete(&membership)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error adding user %s to group %s in proxmox: %v", userID, group.Name, err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"result": "User added to group successfully"})
}

// RemoveUserFromGroup removes a user from a group (admin only)
func RemoveUserFromGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	group, err := getGroupObjectFromRequest(c)
	if err != nil {
		return
	}

	userID := c.Param("userID")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User ID is required"})
		return
	}

	// Check if user exists
	var user UserObject
	if err := db.First(&user, "user_id = ?", userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("User %s not found", userID)})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding user %s: %v", userID, err)})
		}
		return
	}

	// Remove user from group in proxmox
	err = removeUserFromGroupInProxmox(user.ProxmoxUsername, "pam", group.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error removing user %s from group %s in proxmox: %v", userID, group.Name, err)})
		return
	}

	// Remove user from group
	result := db.Where("user_id = ? AND group_id = ?", userID, group.ID).Delete(&UserGroupMembership{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error removing user %s from group %s: %v", userID, group.Name, result.Error)})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("User %s is not a member of group %s", userID, group.Name)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "User removed from group successfully"})
}

// AddRangeToGroup grants group access to a range (admin only)
func AddRangeToGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	group, err := getGroupObjectFromRequest(c)
	if err != nil {
		return
	}

	rangeID := c.Param("rangeID")

	// Check if range exists
	var rangeObj RangeObject
	if err := db.Where("range_id = ?", rangeID).First(&rangeObj).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Range %s not found", rangeID)})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding range %s: %v", rangeID, err)})
		}
		return
	}

	// Check if group already has access to this range
	var existingAccess GroupRangeAccess
	if err := db.Where("group_id = ? AND range_number = ?", group.ID, rangeObj.RangeNumber).First(&existingAccess).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Group %s already has access to range %s", group.Name, rangeObj.RangeID)})
		return
	}

	// Grant group access to range
	groupRangeAccess := GroupRangeAccess{
		GroupID:     uint(group.ID),
		RangeNumber: int32(rangeObj.RangeNumber),
	}

	// Grant group access to range in proxmox
	err = grantGroupAccessToRangeInProxmox(group.Name, rangeObj.RangeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error granting group %s access to range %s in proxmox: %v", group.Name, rangeObj.RangeID, err)})
		return
	}

	if err := db.Create(&groupRangeAccess).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error granting group %s access to range %s: %v", group.Name, rangeObj.RangeID, err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"result": fmt.Sprintf("Group %s access to range %s granted successfully", group.Name, rangeObj.RangeID)})
}

// RemoveRangeFromGroup revokes group access from a range (admin only)
func RemoveRangeFromGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	group, err := getGroupObjectFromRequest(c)
	if err != nil {
		return
	}

	rangeID := c.Param("rangeID")

	var rangeObj RangeObject
	if err := db.Where("range_id = ?", rangeID).First(&rangeObj).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Range %s not found", rangeID)})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding range %s: %v", rangeID, err)})
		}
		return
	}

	// Remove group access to range in proxmox
	err = revokeGroupAccessToRangeInProxmox(group.Name, rangeObj.RangeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error revoking group %s access to range %s in proxmox: %v", group.Name, rangeObj.RangeID, err)})
		return
	}

	// Remove group access to range
	result := db.Where("group_id = ? AND range_number = ?", group.ID, rangeObj.RangeNumber).Delete(&GroupRangeAccess{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error revoking group %s access to range %s: %v", group.Name, rangeObj.RangeID, result.Error)})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Group %s does not have access to range %s", group.Name, rangeObj.RangeID)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "Group access to range revoked successfully"})
}

// ListGroupMembers lists users in a group (admin only)
func ListGroupMembers(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	group, err := getGroupObjectFromRequest(c)
	if err != nil {
		return
	}

	// Get group members
	var memberships []UserGroupMembership
	if err := db.Where("group_id = ?", group.ID).Find(&memberships).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error getting group members: %v", err)})
		return
	}

	// Get user details for each member
	var users []UserObject
	for _, membership := range memberships {
		var user UserObject
		if err := db.First(&user, "user_id = ?", membership.UserID).Error; err == nil {
			users = append(users, user)
		}
	}

	c.JSON(http.StatusOK, gin.H{"result": users})
}

// ListGroupRanges lists ranges accessible to a group (admin only)
func ListGroupRanges(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	group, err := getGroupObjectFromRequest(c)
	if err != nil {
		return
	}

	// Get group range access
	var groupRangeAccesses []GroupRangeAccess
	if err := db.Where("group_id = ?", group.ID).Find(&groupRangeAccesses).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error getting group ranges: %v", err)})
		return
	}

	// Get range details for each accessible range
	var ranges []RangeObject
	for _, access := range groupRangeAccesses {
		var rangeObj RangeObject
		if err := db.Where("range_number = ?", access.RangeNumber).First(&rangeObj).Error; err == nil {
			ranges = append(ranges, rangeObj)
		}
	}

	c.JSON(http.StatusOK, gin.H{"result": ranges})
}
