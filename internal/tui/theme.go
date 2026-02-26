package tui

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	Border    lipgloss.Style
	User      lipgloss.Style
	Assistant lipgloss.Style
	Tool      lipgloss.Style
	Input     lipgloss.Style
	Footer    lipgloss.Style
	Error     lipgloss.Style
	Hint      lipgloss.Style
}

func DefaultTheme() Theme {
	return Theme{
		Border: lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")),
		User: lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		Assistant: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Tool: lipgloss.NewStyle().Foreground(lipgloss.Color("220")),
		Input: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Footer: lipgloss.NewStyle().Foreground(lipgloss.Color("246")),
		Error: lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true),
		Hint: lipgloss.NewStyle().Foreground(lipgloss.Color("241")),
	}
}
