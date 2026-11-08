// SPDX-License-Identifier: MIT

package core

import (
	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/filter"
	"github.com/sichkarmg/vaultcrab/internal/hostlist"
	"github.com/sichkarmg/vaultcrab/internal/launchers"
	"github.com/sichkarmg/vaultcrab/internal/layout"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/scripts"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
	"github.com/sichkarmg/vaultcrab/internal/term"
	"github.com/sichkarmg/vaultcrab/internal/vault"
	"github.com/sichkarmg/vaultcrab/internal/widget"
	"github.com/sichkarmg/vaultcrab/internal/workspace"
)

// Re-exports so that the UI depends on core only.

type (
	Host           = model.Host
	Snapshot       = model.Snapshot
	Folder         = model.Folder
	Org            = model.Org
	Collection     = model.Collection
	Scheme         = model.Scheme
	Session        = session.Session
	SessionState   = session.State
	Key            = term.Key
	Mouse          = term.Mouse
	RenderOpts     = term.RenderOptions
	Rule           = filter.Rule
	Filter         = filter.Filter
	ListOptions    = hostlist.Options
	Row            = hostlist.Row
	Grouping       = hostlist.Grouping
	IconSet        = config.IconSet
	VaultInfo      = vault.StatusInfo
	Tab            = workspace.Tab
	PaneID         = layout.PaneID
	LayoutNode     = layout.Node
	Geometry       = layout.Geometry
	Rect           = layout.Rect
	SplitDir       = layout.Dir
	Side           = layout.Side
	Secret         = secret.Secret
	LocalOptions   = connect.LocalOptions
	Script         = scripts.Script
	TermPos        = term.Pos
	HostEdit       = vault.HostEdit
	TabState       = workspace.TabState
	PaneState      = workspace.PaneState
	WorkspaceState = workspace.State
	ViewState      = workspace.ViewState
	WidgetOutput   = widget.Output
	WidgetSpec     = widget.Spec
	Appearance     = config.Appearance
	VaultSpec      = config.VaultSpec
	Unlock         = config.Unlock
	Icon           = config.Icon
	Launchers      = config.Launchers
	Launch         = config.Launch
	ScriptSpec     = config.ScriptSpec
	ClientInfo     = launchers.Client
	ClientPreset   = launchers.Preset
	FoundClient    = launchers.Found
)

// The External programs page of the settings.
var (
	OSes          = config.OSes
	VNCModes      = connect.VNCModes
	Placeholders  = launchers.Placeholders
	KnownClients  = launchers.Clients
	CheckArgs     = launchers.CheckArgs
	PreviewCmd    = launchers.Preview
	MaskedSecret  = launchers.Masked
	ArgvWarning   = launchers.ArgvWarning
	VNCProxyMode  = connect.VNCProxy
	PlaceholderOf = connect.VNCPlaceholder
)

// LongLockTimeout is the longest lock timeout without a warning.
const LongLockTimeout = config.LongLockTimeout

// WidgetClock is the built-in clock widget (WidgetSpec.Builtin).
const WidgetClock = widget.BuiltinClock

// Appearance settings: choices offered in the settings dialog, and saving
// the changed ones into the config file.
var (
	SSHIcons        = config.SSHIcons
	Accents         = config.Accents
	AccentColor     = config.AccentColor
	Selections      = config.Selections
	SelectionColors = config.SelectionColors
	SaveAppearance  = config.SaveAppearance
	// The lock timeouts (Settings → Security).
	LockTimeouts     = config.LockTimeouts
	LockTimeoutLabel = config.LockTimeoutLabel
	// Vaults in the config file (Settings → Vaults).
	AddVaultConfig    = config.AddVault
	RemoveVaultConfig = config.RemoveVault
)

// Layout directions.
const (
	SplitRight = layout.Horizontal
	SplitDown  = layout.Vertical
	SideLeft   = layout.Left
	SideRight  = layout.Right
	SideUp     = layout.Up
	SideDown   = layout.Down
)

// SecretFromString makes a credential (for fake sources in tests).
func SecretFromString(s string) Secret { return secret.FromString(s) }

// Schemes are the connection types of vault items in display order.
var Schemes = model.Schemes

// SaveWorkspace and LoadWorkspace store the open tabs (host IDs and the
// pane layout only) between runs.
var (
	SaveWorkspace = workspace.SaveState
	LoadWorkspace = workspace.LoadState
	SaveView      = workspace.SaveView
	LoadView      = workspace.LoadView
)

// RestoreTab rebuilds a saved tab; open returns the session of a pane or nil.
func RestoreTab(ts TabState, open func(PaneState) *Session) *Tab { return workspace.Restore(ts, open) }

// NewTab creates a tab (pane layout) showing s.
func NewTab(s *Session) *Tab { return workspace.New(s) }

// Vault statuses reported by a Source.
const (
	VaultUnauthenticated = vault.Unauthenticated
	VaultLocked          = vault.Locked
	VaultUnlocked        = vault.Unlocked
)

const (
	SchemeSSH   = model.SSH
	SchemeVNC   = model.VNC
	SchemeSMB   = model.SMB
	SchemeFTP   = model.FTP
	SchemeSFTP  = model.SFTP
	SchemeRDP   = model.RDP
	SchemeLocal = model.Local

	ScriptExternal = scripts.External
	ScriptTerminal = scripts.Terminal

	SessionIdle         = session.Idle
	SessionConnecting   = session.Connecting
	SessionConnected    = session.Connected
	SessionDisconnected = session.Disconnected
	SessionClosed       = session.Closed

	KeyUp        = term.KeyUp
	KeyDown      = term.KeyDown
	KeyRight     = term.KeyRight
	KeyLeft      = term.KeyLeft
	KeyHome      = term.KeyHome
	KeyEnd       = term.KeyEnd
	KeyPgUp      = term.KeyPgUp
	KeyPgDown    = term.KeyPgDown
	KeyInsert    = term.KeyInsert
	KeyDelete    = term.KeyDelete
	KeyEnter     = term.KeyEnter
	KeyTab       = term.KeyTab
	KeyBackspace = term.KeyBackspace
	KeyEscape    = term.KeyEscape
	KeySpace     = term.KeySpace
	KeyF1        = term.KeyF1

	MouseNone      = term.MouseNone
	MouseLeft      = term.MouseLeft
	MouseMiddle    = term.MouseMiddle
	MouseRight     = term.MouseRight
	MouseWheelUp   = term.MouseWheelUp
	MouseWheelDown = term.MouseWheelDown
	MousePress     = term.MousePress
	MouseRelease   = term.MouseRelease
	MouseMotion    = term.MouseMotion

	DefaultScrollback = term.DefaultScrollback

	GroupRow     = hostlist.GroupRow
	HostRow      = hostlist.HostRow
	ByFolder     = hostlist.ByFolder
	ByCollection = hostlist.ByCollection
	Flat         = hostlist.Flat
	OrgAll       = hostlist.OrgAll
	OrgPersonal  = hostlist.OrgPersonal
)

// Icon names and key actions (from config).
const (
	IconConnected    = config.IconConnected
	IconConnecting   = config.IconConnecting
	IconDisconnected = config.IconDisconnected
	IconIdle         = config.IconIdle
	IconSSH          = config.IconSSH
	DefaultTabWidth  = config.DefaultTabWidth
	IconVNC          = config.IconVNC
	IconSMB          = config.IconSMB
	IconFTP          = config.IconFTP
	IconSFTP         = config.IconSFTP
	IconRDP          = config.IconRDP
	IconLocal        = config.IconLocal
	IconQuit         = config.IconQuit
	IconSettings     = config.IconSettings
	IconLog          = config.IconLog
	IconFolder       = config.IconFolder
	IconFolderOpen   = config.IconFolderOpen
	IconOrg          = config.IconOrg
	IconCollection   = config.IconCollection
	IconOnline       = config.IconOnline
	IconOffline      = config.IconOffline
	IconLocked       = config.IconLocked
	IconSyncing      = config.IconSyncing
	IconFav          = config.IconFav
	IconZoom         = config.IconZoom
	IconHistory      = config.IconHistory
	IconCached       = config.IconCached

	ActNextTab    = config.ActNextTab
	ActPrevTab    = config.ActPrevTab
	ActCloseTab   = config.ActCloseTab
	ActCloneTab   = config.ActCloneTab
	ActShowLog    = config.ActShowLog
	ActList       = config.ActList
	ActHistory    = config.ActHistory
	ActSplitRight = config.ActSplitRight
	ActSplitDown  = config.ActSplitDown
	ActClosePane  = config.ActClosePane
	ActZoom       = config.ActZoom
	ActFocusLeft  = config.ActFocusLeft
	ActFocusRight = config.ActFocusRight
	ActFocusUp    = config.ActFocusUp
	ActFocusDown  = config.ActFocusDown
	ActGrowLeft   = config.ActGrowLeft
	ActGrowRight  = config.ActGrowRight
	ActGrowUp     = config.ActGrowUp
	ActGrowDown   = config.ActGrowDown
	ActBreakPane  = config.ActBreakPane
	ActReconnect  = config.ActReconnect
	ActQuit       = config.ActQuit
	ActHelp       = config.ActHelp
	ActLocal      = config.ActLocal
	ActSettings   = config.ActSettings
)
