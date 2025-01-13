package deej

import _ "embed"

// DeejLogo is a binary representation of the deej logo; used for notifications and tray icon
//go:embed assets/logo.ico
var DeejLogo []byte

// EditConfig is the cog icon in the edit config menu option
//go:embed assets/menu-items/edit-config.ico
var EditConfigIcon []byte

// RefreshSessions is the reload icon in the refresh sessions menu option
//go:embed assets/menu-items/refresh-sessions.ico
var RefreshSessionsIcon []byte
