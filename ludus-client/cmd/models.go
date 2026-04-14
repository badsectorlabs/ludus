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
	IsRouter    bool   `json:"isRouter"`
}

type AnsibleItem struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"type"`
	Global  bool   `json:"global"`
}

type RangeObject struct {
	RangeID        string     `json:"rangeID"`
	RangeNumber    int32      `json:"rangeNumber"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Purpose        string     `json:"purpose"`
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
	UserNumber      int32     `json:"userNumber"`
	DateCreated     time.Time `json:"dateCreated"`
	DateLastActive  time.Time `json:"dateLastActive"`
	IsAdmin         bool      `json:"isAdmin"`
	ProxmoxUsername string    `json:"proxmoxUsername"`
	QuotaRAM        int       `json:"quotaRAM,omitempty"`
	QuotaCPU        int       `json:"quotaCPU,omitempty"`
	QuotaVMs        int       `json:"quotaVMs,omitempty"`
	QuotaRanges     int       `json:"quotaRanges,omitempty"`
}

type RangeAccessObject struct {
	TargetUserID  string   `json:"targetUserID"`
	SourceUserIDs []string `json:"sourceUserIDs"`
}

type QuotaStatusObject struct {
	LimitRAM    int `json:"limitRAM"`
	LimitCPU    int `json:"limitCPU"`
	LimitVMs    int `json:"limitVMs"`
	LimitRanges int `json:"limitRanges"`
	UsedRAM     int `json:"usedRAM"`
	UsedCPU     int `json:"usedCPU"`
	UsedVMs     int `json:"usedVMs"`
	UsedRanges  int `json:"usedRanges"`
}

type AllQuotaStatusObject struct {
	UserID       string `json:"userID"`
	Name         string `json:"name"`
	LimitRAM     int    `json:"limitRAM"`
	LimitCPU     int    `json:"limitCPU"`
	LimitVMs     int    `json:"limitVMs"`
	LimitRanges  int    `json:"limitRanges"`
	UsedRAM      int    `json:"usedRAM"`
	UsedCPU      int    `json:"usedCPU"`
	UsedVMs      int    `json:"usedVMs"`
	UsedRanges   int    `json:"usedRanges"`
	SourceRAM    string `json:"sourceRAM"`
	SourceCPU    string `json:"sourceCPU"`
	SourceVMs    string `json:"sourceVMs"`
	SourceRanges string `json:"sourceRanges"`
}

type GroupQuotaObject struct {
	Name               string `json:"name"`
	DefaultQuotaRAM    int    `json:"defaultQuotaRAM"`
	DefaultQuotaCPU    int    `json:"defaultQuotaCPU"`
	DefaultQuotaVMs    int    `json:"defaultQuotaVMs"`
	DefaultQuotaRanges int    `json:"defaultQuotaRanges"`
	MemberCount        int    `json:"memberCount"`
}
