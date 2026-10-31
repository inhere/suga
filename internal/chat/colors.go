package chat

import "github.com/inhere/suga/pkg/chatui"

// chat.colors is the styling of chat's own lines; the palette and the tool
// families live in pkg/chatui (palette.go). These names keep chat's own code
// short.

type (
	palette    = chatui.Palette
	toolFamily = chatui.ToolFamily
)

const (
	familyRead  = chatui.FamilyRead
	familyMeta  = chatui.FamilyMeta
	familyCount = chatui.FamilyCount

	colorsKeyMode    = chatui.ColorsKeyMode
	colorsKeyStyle   = chatui.ColorsKeyStyle
	colorModeANSI    = chatui.ColorModeANSI
	colorMode256     = chatui.ColorMode256
	colorModeTrue    = chatui.ColorModeTrue
	colorModeOff     = chatui.ColorModeOff
	colorStyleSymbol = chatui.ColorStyleSymbol
	colorStyleLine   = chatui.ColorStyleLine
	themeLight       = chatui.ThemeLight
)

var (
	familySymbols = chatui.FamilySymbols
	familyNames   = chatui.FamilyNames
	toolFamilyOf  = chatui.ToolFamilyOf
	newPalette    = chatui.NewPalette
)
