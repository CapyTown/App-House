package adb

type Device struct {
	Serial         string
	State          string
	Model          string
	Manufacturer   string
	Brand          string
	MarketName     string
	AndroidVersion string
	SDK            string
}

// DeviceInfo contains the device's own reported product and Android metadata.
type DeviceInfo struct {
	Model          string
	Manufacturer   string
	Brand          string
	MarketName     string
	AndroidVersion string
	SDK            string
}

type User struct {
	ID   int
	Name string
}

type App struct {
	PackageName string       `json:"packageName"`
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	System      bool         `json:"system"`
	Enabled     bool         `json:"enabled"`
	Suspended   bool         `json:"suspended"`
	Icon        string       `json:"icon"`
	Permissions []Permission `json:"permissions"`
	Signers     []Signer     `json:"signers"`
	Warnings    []string     `json:"warnings"`
}

type Permission struct {
	Name    string `json:"name"`
	Granted bool   `json:"granted"`
}

type Signer struct {
	Subject      string `json:"subject"`
	Organization string `json:"organization"`
	SHA256       string `json:"sha256"`
}

type Action string

const (
	Uninstall Action = "uninstall"
	Suspend   Action = "suspend"
	Unsuspend Action = "unsuspend"
	Disable   Action = "disable-user"
	Enable    Action = "enable"
)
