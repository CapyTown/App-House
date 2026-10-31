package preferences

// MirrorOptions stores the user's scrcpy choices independently of a running session.
type MirrorOptions struct {
	Path               string `json:"path,omitempty"`
	CaptureDirectory   string `json:"captureDirectory,omitempty"`
	AskCaptureLocation bool   `json:"askCaptureLocation,omitzero"`
	StayAwake          bool   `json:"stayAwake,omitzero"`
	KeepActive         bool   `json:"keepActive,omitzero"`
	TurnScreenOff      bool   `json:"turnScreenOff,omitzero"`
	AlwaysOnTop        bool   `json:"alwaysOnTop,omitzero"`
	NoAudio            bool   `json:"noAudio,omitzero"`
	ShowTouches        bool   `json:"showTouches,omitzero"`
	DisableScreensaver bool   `json:"disableScreensaver,omitzero"`
	MaxSize            int    `json:"maxSize,omitzero"`
	MaxFPS             int    `json:"maxFPS,omitzero"`
	VideoBitRate       int    `json:"videoBitRate,omitzero"`
}
