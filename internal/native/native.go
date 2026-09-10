// Package native runs the macOS image-window frontend.
package native

// Action controls how the reading position is handled when the window closes.
type Action int

const (
	ActionClear Action = iota
	ActionSave
	ActionReturn
)

// State holds the reading position and view settings for both frontends.
type State struct {
	Path             string
	Page             int
	Offset           float64
	Scroll           float64
	Webtoon          bool
	Rotation         int
	Zoom             float64
	CenterX, CenterY float64
	Spread           bool
	Inverted         bool
}

// Result contains the final native-window state and requested exit action.
type Result struct {
	State  State
	Action Action
}
