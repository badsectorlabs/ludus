package ludusapi

import (
	"database/sql/driver"
	"strings"
	"time"
)

// SQLite does not support array types, so we will make our own
// Since domains and IPs cannot contain "|" we'll use that as our
// separator and serialize and deserialize with that
type SQLiteStringArray []string

func (a *SQLiteStringArray) Scan(value interface{}) error {
	stringValue := value.(string)
	stringArray := strings.Split(stringValue, "|")
	*a = stringArray
	return nil
}

func (a SQLiteStringArray) Value() (driver.Value, error) {
	stringValue := strings.Join(a, "|")
	return stringValue, nil
}

type RangeObject struct {

	// Must be a unique 2-4 letter uppercase string.  Initials are commonly used.
	UserID string `json:"userID" gorm:"unique"`

	RangeNumber int32 `json:"rangeNumber" gorm:"primaryKey"`

	LastDeployment time.Time `json:"lastDeployment"`

	NumberOfVMs int32 `json:"numberOfVMs" gorm:"default:0"`

	TestingEnabled bool `json:"testingEnabled" gorm:"default:false"`

	VMs []VmObject `json:"VMs" gorm:"foreignKey:RangeNumber;references:RangeNumber"`

	AllowedDomains SQLiteStringArray `json:"allowedDomains" gorm:"type:string"`

	AllowedIPs SQLiteStringArray `json:"allowedIPs" gorm:"type:string"`

	RangeState string `json:"rangeState" gorm:"default:'NEVER DEPLOYED'"`
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

	// Must be a unique 2-4 letter uppercase string.  Initials are commonly used.
	UserID string `json:"userID" gorm:"primaryKey"`

	DateCreated time.Time `gorm:"autoCreateTime" json:"dateCreated"`

	DateLastActive time.Time `json:"dateLastActive"`

	IsAdmin bool `json:"isAdmin"`

	HashedAPIKey string `json:"-"` // - means do not marshal as JSON, prevents the hash from being sent with every user object

	ProxmoxUsername string `json:"proxmoxUsername"`
}

type VmObject struct {
	ID uint `gorm:"primaryKey"`

	ProxmoxID int32 `json:"proxmoxID"`

	RangeNumber int32 `json:"rangeNumber" gorm:"foreignKey"`

	Name string `json:"name"`

	PoweredOn bool `json:"poweredOn"`

	IP string `json:"ip,omitempty"`
}
