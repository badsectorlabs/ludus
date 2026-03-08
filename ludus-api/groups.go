package ludusapi

import (
	"ludusapi/models"
)

func userIsManagerOfGroup(user *models.User, group *models.Group) bool {
	userIsManager := false
	for _, manager := range group.Managers() {
		if manager.Id == user.Id {
			userIsManager = true
			break
		}
	}
	return userIsManager
}
