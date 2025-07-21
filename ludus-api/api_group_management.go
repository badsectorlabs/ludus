package ludusapi

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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

	c.JSON(http.StatusCreated, gin.H{"result": group})
}

// DeleteGroup deletes a group and cleans up memberships (admin only)
func DeleteGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	groupIDStr := c.Param("groupID")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	var group GroupObject
	if err := db.First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding group: %v", err)})
		}
		return
	}

	// Clean up all memberships and range access for this group
	db.Where("group_id = ?", groupID).Delete(&UserGroupMembership{})
	db.Where("group_id = ?", groupID).Delete(&GroupRangeAccess{})

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

	groupIDStr := c.Param("groupID")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	userID := c.Param("userID")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User ID is required"})
		return
	}

	// Check if group exists
	var group GroupObject
	if err := db.First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding group: %v", err)})
		}
		return
	}

	// Check if user exists
	var user UserObject
	if err := db.First(&user, "user_id = ?", userID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding user: %v", err)})
		}
		return
	}

	// Check if user is already a member
	var existingMembership UserGroupMembership
	if err := db.Where("user_id = ? AND group_id = ?", userID, groupID).First(&existingMembership).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User is already a member of this group"})
		return
	}

	// Add user to group
	membership := UserGroupMembership{
		UserID:  userID,
		GroupID: uint(groupID),
	}

	if err := db.Create(&membership).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error adding user to group: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"result": "User added to group successfully"})
}

// RemoveUserFromGroup removes a user from a group (admin only)
func RemoveUserFromGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	groupIDStr := c.Param("groupID")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	userID := c.Param("userID")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User ID is required"})
		return
	}

	// Remove user from group
	result := db.Where("user_id = ? AND group_id = ?", userID, groupID).Delete(&UserGroupMembership{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error removing user from group: %v", result.Error)})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "User is not a member of this group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "User removed from group successfully"})
}

// AddRangeToGroup grants group access to a range (admin only)
func AddRangeToGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	groupIDStr := c.Param("groupID")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	rangeNumberStr := c.Param("rangeNumber")
	rangeNumber, err := strconv.ParseInt(rangeNumberStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid range number"})
		return
	}

	// Check if group exists
	var group GroupObject
	if err := db.First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding group: %v", err)})
		}
		return
	}

	// Check if range exists
	var rangeObj RangeObject
	if err := db.Where("range_number = ?", rangeNumber).First(&rangeObj).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Range not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding range: %v", err)})
		}
		return
	}

	// Check if group already has access to this range
	var existingAccess GroupRangeAccess
	if err := db.Where("group_id = ? AND range_number = ?", groupID, rangeNumber).First(&existingAccess).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Group already has access to this range"})
		return
	}

	// Grant group access to range
	groupRangeAccess := GroupRangeAccess{
		GroupID:     uint(groupID),
		RangeNumber: int32(rangeNumber),
	}

	if err := db.Create(&groupRangeAccess).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error granting group access to range: %v", err)})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"result": "Group access to range granted successfully"})
}

// RemoveRangeFromGroup revokes group access from a range (admin only)
func RemoveRangeFromGroup(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	groupIDStr := c.Param("groupID")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	rangeNumberStr := c.Param("rangeNumber")
	rangeNumber, err := strconv.ParseInt(rangeNumberStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid range number"})
		return
	}

	// Remove group access to range
	result := db.Where("group_id = ? AND range_number = ?", groupID, rangeNumber).Delete(&GroupRangeAccess{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error revoking group access to range: %v", result.Error)})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group does not have access to this range"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "Group access to range revoked successfully"})
}

// ListGroupMembers lists users in a group (admin only)
func ListGroupMembers(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	groupIDStr := c.Param("groupID")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	// Check if group exists
	var group GroupObject
	if err := db.First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding group: %v", err)})
		}
		return
	}

	// Get group members
	var memberships []UserGroupMembership
	if err := db.Where("group_id = ?", groupID).Find(&memberships).Error; err != nil {
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

	groupIDStr := c.Param("groupID")
	groupID, err := strconv.ParseUint(groupIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})
		return
	}

	// Check if group exists
	var group GroupObject
	if err := db.First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error finding group: %v", err)})
		}
		return
	}

	// Get group range access
	var groupRangeAccesses []GroupRangeAccess
	if err := db.Where("group_id = ?", groupID).Find(&groupRangeAccesses).Error; err != nil {
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
