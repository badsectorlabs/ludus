package dto

import "time"

/*
	 {
	  "name": "John Doe",
	  "userID": "JD",
	  "dateCreated": "2022-08-29T09:12:33.001Z",
	  "dateLastActive": "2022-08-29T09:12:33.001Z",
	  "isAdmin": true,
	  "proxmoxUsername": "john-doe",
	  "apiKey": "JD.Vf{M@GC:w}YQ=1zv@gLLnDH:j3nI]l7@:ct:qPy9"
	}
*/
type AddUserResponse struct {
	Name            string    `json:"name"`
	UserID          string    `json:"userID"`
	DateCreated     time.Time `json:"dateCreated"`
	DateLastActive  time.Time `json:"dateLastActive"`
	IsAdmin         bool      `json:"isAdmin"`
	ProxmoxUsername string    `json:"proxmoxUsername"`
	APIKey          string    `json:"apiKey"`
}
