package main

import "os/user"

func userExists(username string) bool {
	_, err := user.Lookup(username)
	if err != nil {
		if _, ok := err.(user.UnknownUserError); ok {
			return false
		}
	}
	return true
}
