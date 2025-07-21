package ludusapi

import (
	"time"
)

type RangeObject struct {
	// Must be a unique 2-4 letter uppercase string.  Initials are commonly used.
	UserID string `json:"userID" gorm:"index"` // Removed unique constraint to allow multiple ranges per user

	RangeNumber int32 `json:"rangeNumber" gorm:"primaryKey"`

	// New fields for range metadata
	Name        string `json:"name" gorm:"not null"`
	Description string `json:"description"`
	Purpose     string `json:"purpose"`

	LastDeployment time.Time `json:"lastDeployment"`

	NumberOfVMs int32 `json:"numberOfVMs" gorm:"default:0"`

	TestingEnabled bool `json:"testingEnabled" gorm:"default:false"`

	VMs []VmObject `json:"VMs" gorm:"foreignKey:RangeNumber;references:RangeNumber"`

	AllowedDomains []string `json:"allowedDomains" gorm:"type:text[]"` // PostgreSQL array type

	AllowedIPs []string `json:"allowedIPs" gorm:"type:text[]"` // PostgreSQL array type

	RangeState string `json:"rangeState" gorm:"default:'NEVER DEPLOYED'"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type UserApiKeyObject struct {
	ApiKey string `json:"apiKey,omitempty"`
}

type UserCredentialObject struct {
	ProxmoxUsername string `json:"proxmoxUsername,omitempty"`

	ProxmoxPassword string `json:"proxmoxPassword,omitempty"`
}

type UserObject struct {
	Name string `json:"name" gorm:"not null"`

	// Must be a unique 2-20 letter uppercase string. Initials are commonly used.
	UserID string `json:"userID" gorm:"primaryKey"`

	DateCreated time.Time `gorm:"autoCreateTime" json:"dateCreated"`

	DateLastActive time.Time `json:"dateLastActive"`

	IsAdmin bool `json:"isAdmin"`

	HashedAPIKey string `json:"-"` // - means do not marshal as JSON, prevents the hash from being sent with every user object

	ProxmoxUsername string `json:"proxmoxUsername"`

	PortforwardingEnabled bool `json:"portforwardingEnabled"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

type VmObject struct {
	ID uint `gorm:"primaryKey"`

	ProxmoxID int32 `json:"proxmoxID"`

	RangeNumber int32 `json:"rangeNumber" gorm:"foreignKey"`

	Name string `json:"name"`

	PoweredOn bool `json:"poweredOn"`

	IP string `json:"ip,omitempty"`

	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

// New models for group-based access system
type GroupObject struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name" gorm:"unique;not null"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt   time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

// UserRangeAccess represents direct user-to-range assignments
type UserRangeAccess struct {
	UserID      string    `json:"userID" gorm:"primaryKey"`
	RangeNumber int32     `json:"rangeNumber" gorm:"primaryKey"`
	CreatedAt   time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

// UserGroupMembership represents user membership in groups
type UserGroupMembership struct {
	UserID    string    `json:"userID" gorm:"primaryKey"`
	GroupID   uint      `json:"groupID" gorm:"primaryKey"`
	CreatedAt time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

// GroupRangeAccess represents group access to ranges
type GroupRangeAccess struct {
	GroupID     uint      `json:"groupID" gorm:"primaryKey"`
	RangeNumber int32     `json:"rangeNumber" gorm:"primaryKey"`
	CreatedAt   time.Time `json:"createdAt" gorm:"autoCreateTime"`
}

// Keep RangeAccessObject for backward compatibility during migration
type RangeAccessObject struct {
	TargetUserID  string   `json:"targetUserID" gorm:"primaryKey"`
	SourceUserIDs []string `json:"sourceUserIDs" gorm:"type:text[]"` // Updated to PostgreSQL array
}
