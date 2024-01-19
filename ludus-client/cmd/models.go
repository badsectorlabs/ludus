package cmd

import (
	"time"
)

type VmObject struct {
	ID          int32  `json:"ID"`
	ProxmoxID   int32  `json:"proxmoxID"`
	RangeNumber int32  `json:"rangeNumber"`
	Name        string `json:"name"`
	PoweredOn   bool   `json:"poweredOn"`
	Ip          string `json:"ip,omitempty"`
}

type AnsibleItem struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type"`
}

type RangeObject struct {
	UserID         string     `json:"userID"`
	RangeNumber    int32      `json:"rangeNumber"`
	LastDeployment time.Time  `json:"lastDeployment"`
	NumberOfVMs    int32      `json:"numberOfVMs"`
	TestingEnabled bool       `json:"testingEnabled"`
	AllowedIPs     []string   `json:"allowedIPs"`
	AllowedDomains []string   `json:"allowedDomains"`
	VMs            []VmObject `json:"VMs"`
	RangeState     string     `json:"rangeState"`
}

type UserObject struct {
	Name            string    `json:"name"`
	UserID          string    `json:"userID"`
	DateCreated     time.Time `json:"dateCreated"`
	DateLastActive  time.Time `json:"dateLastActive"`
	IsAdmin         bool      `json:"isAdmin"`
	ProxmoxUsername string    `json:"proxmoxUsername"`
}
