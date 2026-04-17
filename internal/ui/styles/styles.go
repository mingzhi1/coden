package styles

import "charm.land/lipgloss/v2"

var (
	ColorPrimary     = lipgloss.Color("#5e81ac")
	ColorSecondary   = lipgloss.Color("#88c0d0")
	ColorText        = lipgloss.Color("#eceff4")
	ColorMuted       = lipgloss.Color("#4c566a")
	ColorBg          = lipgloss.Color("#2e3440")
	ColorBgElevated  = lipgloss.Color("#242933")
	ColorBgHighlight = lipgloss.Color("#3b4252")

	ColorError   = lipgloss.Color("#bf616a")
	ColorWarning = lipgloss.Color("#ebcb8b")
	ColorSuccess = lipgloss.Color("#a3be8c")
)

var (
	NormalText = lipgloss.NewStyle().Foreground(ColorText)
	MutedText  = lipgloss.NewStyle().Foreground(ColorMuted)
	BoldText   = NormalText.Bold(true)

	PrimaryText = lipgloss.NewStyle().Foreground(ColorPrimary)
	ErrorText   = lipgloss.NewStyle().Foreground(ColorError)
	WarningText = lipgloss.NewStyle().Foreground(ColorWarning)
	SuccessText = lipgloss.NewStyle().Foreground(ColorSuccess)

	StatusBar = lipgloss.NewStyle().
			Background(ColorBgHighlight).
			Foreground(ColorText).
			Padding(0, 1)

	Panel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorMuted).
		Padding(0, 1)

	PanelFocus = Panel.BorderForeground(ColorPrimary)

	ChatUserLabel      = lipgloss.NewStyle().Foreground(ColorBg).Background(ColorSecondary).Bold(true).Padding(0, 1)
	ChatAssistantLabel = lipgloss.NewStyle().Foreground(ColorBg).Background(ColorSuccess).Bold(true).Padding(0, 1)
	ChatSystemLabel    = lipgloss.NewStyle().Foreground(ColorBg).Background(ColorMuted).Bold(true).Padding(0, 1)
	ChatPlanLabel      = lipgloss.NewStyle().Foreground(ColorBg).Background(ColorPrimary).Bold(true).Padding(0, 1)
	ChatToolLabel      = lipgloss.NewStyle().Foreground(ColorBg).Background(ColorWarning).Bold(true).Padding(0, 1)
	ChatEditLabel      = lipgloss.NewStyle().Foreground(ColorBg).Background(ColorError).Bold(true).Padding(0, 1)
	ChatWarnLabel      = lipgloss.NewStyle().Foreground(ColorBg).Background(ColorWarning).Bold(true).Padding(0, 1)
	ChatErrorLabel     = lipgloss.NewStyle().Foreground(ColorText).Background(ColorError).Bold(true).Padding(0, 1)

	ChatUserAccent      = lipgloss.NewStyle().Foreground(ColorSecondary)
	ChatAssistantAccent = lipgloss.NewStyle().Foreground(ColorSuccess)
	ChatSystemAccent    = lipgloss.NewStyle().Foreground(ColorMuted)
	ChatPlanAccent      = lipgloss.NewStyle().Foreground(ColorPrimary)
	ChatToolAccent      = lipgloss.NewStyle().Foreground(ColorWarning)
	ChatEditAccent      = lipgloss.NewStyle().Foreground(ColorError)
	ChatWarnAccent      = lipgloss.NewStyle().Foreground(ColorWarning)
	ChatErrorAccent     = lipgloss.NewStyle().Foreground(ColorError)

	MarkdownHeading = lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true)
	MarkdownBullet  = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	MarkdownQuote   = lipgloss.NewStyle().Foreground(ColorWarning)
	MarkdownCode    = lipgloss.NewStyle().Foreground(ColorSuccess)
	MarkdownInlineCode = lipgloss.NewStyle().Foreground(ColorSuccess).Background(ColorBgHighlight)
)
