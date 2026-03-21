package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/0xADE/adm/internal/dm"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("62"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

type phase int

const (
	phaseLogin phase = iota
	phaseSession
)

type deskItem struct {
	d *dm.Desktop
}

func (i deskItem) Title() string       { return i.d.Name() }
func (i deskItem) Description() string { return i.d.EnvLabel() }
func (i deskItem) FilterValue() string { return i.d.Name() }

// rootModel drives login and session selection in one program.
type rootModel struct {
	conf   *dm.Config
	motd   string
	h      *dm.SessionHandle
	errMsg string
	auth   dm.AuthHandle

	userIn textinput.Model
	passIn textinput.Model
	focus  int

	sessList list.Model
	desktops []*dm.Desktop
	lastIdx  int
	phase    phase
}

// NewRoot creates the Bubble Tea model for one login + session cycle.
func NewRoot(conf *dm.Config, motd string, h *dm.SessionHandle) tea.Model {
	tu := textinput.New()
	tu.Placeholder = "username"
	tu.CharLimit = 256
	tu.Width = 40
	tu.Focus()

	tp := textinput.New()
	tp.Placeholder = "password"
	tp.EchoMode = textinput.EchoPassword
	tp.CharLimit = 256
	tp.Width = 40

	if conf.DefaultUser != "" {
		tu.SetValue(conf.DefaultUser)
	} else if last := dm.LastUserHint(conf); last != "" {
		tu.SetValue(last)
	}

	return &rootModel{
		conf:   conf,
		motd:   motd,
		h:      h,
		userIn: tu,
		passIn: tp,
		phase:  phaseLogin,
	}
}

func (m *rootModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tea.WindowSize())
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			if m.auth != nil {
				m.auth.CloseAuth()
			}
			return m, tea.Quit
		}
	}

	switch m.phase {
	case phaseLogin:
		return m.updateLogin(msg)
	case phaseSession:
		return m.updateSession(msg)
	}
	return m, nil
}

func (m *rootModel) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab":
			if m.focus == 0 {
				m.focus = 1
				m.userIn.Blur()
				m.passIn.Focus()
			} else {
				m.focus = 0
				m.passIn.Blur()
				m.userIn.Focus()
			}
			return m, textinput.Blink
		case "enter":
			user := strings.TrimSpace(m.userIn.Value())
			// Enter on username: run :commands immediately, else move to password.
			if m.focus == 0 {
				if dm.ShouldProcessCommand(user, m.conf) {
					_ = dm.ProcessCommand(dm.FormatCommand(user), m.conf, nil, false)
					return m, tea.Quit
				}
				m.focus = 1
				m.userIn.Blur()
				m.passIn.Focus()
				return m, textinput.Blink
			}
			pass := m.passIn.Value()
			if dm.ShouldProcessCommand(user, m.conf) {
				_ = dm.ProcessCommand(dm.FormatCommand(user), m.conf, nil, false)
				return m, tea.Quit
			}
			auth, err := dm.Authenticate(m.conf, user, pass)
			if err != nil {
				m.errMsg = err.Error()
				return m, nil
			}
			if auth == nil {
				m.errMsg = "authentication failed"
				return m, nil
			}
			if cmd := auth.GetCommand(); cmd != "" {
				_ = dm.ProcessCommand(cmd, m.conf, auth, false)
				auth.CloseAuth()
				return m, tea.Quit
			}
			m.errMsg = ""
			m.auth = auth

			su := dm.UserOf(auth)
			if su == nil {
				m.errMsg = "no user record after authentication (internal error)"
				return m, nil
			}
			ud, _ := dm.LoadUserConfig(su.HomeDir())
			chosen, desktops, lastIdx, needUI := dm.TryAutoSelectDesktop(auth, m.conf, ud)
			m.desktops = desktops
			m.lastIdx = lastIdx

			if ud != nil && ud.SelectionMode() == dm.SelectionFalse {
				d := dm.FinalizeDesktopSelection(auth, m.conf, ud, ud, desktops)
				dm.RunLoginSession(m.conf, m.h, auth, d)
				return m, tea.Quit
			}
			if !needUI && chosen != nil {
				d := dm.FinalizeDesktopSelection(auth, m.conf, ud, chosen, desktops)
				dm.RunLoginSession(m.conf, m.h, auth, d)
				return m, tea.Quit
			}

			items := make([]list.Item, 0, len(desktops))
			for _, d := range desktops {
				dd := d
				items = append(items, deskItem{d: dd})
			}
			delegate := list.NewDefaultDelegate()
			l := list.New(items, delegate, 80, min(22, len(items)+6))
			l.Title = "Session"
			l.Styles.Title = titleStyle
			if lastIdx >= 0 && lastIdx < len(desktops) {
				l.Select(lastIdx)
			}
			m.sessList = l
			m.phase = phaseSession
			return m, nil
		}
	}

	var cmd tea.Cmd
	if m.focus == 0 {
		m.userIn, cmd = m.userIn.Update(msg)
	} else {
		m.passIn, cmd = m.passIn.Update(msg)
	}
	return m, cmd
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m *rootModel) updateSession(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.sessList.SetWidth(msg.Width - 4)
		m.sessList.SetHeight(msg.Height - 10)
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "enter" {
			i := m.sessList.Index()
			if i < 0 || i >= len(m.desktops) {
				return m, nil
			}
			selected := m.desktops[i]
			auth := m.auth
			su := dm.UserOf(auth)
			if su == nil {
				m.errMsg = "no user record (internal error)"
				return m, nil
			}
			ud, _ := dm.LoadUserConfig(su.HomeDir())
			d := dm.FinalizeDesktopSelection(auth, m.conf, ud, selected, m.desktops)
			dm.RunLoginSession(m.conf, m.h, auth, d)
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.sessList, cmd = m.sessList.Update(msg)
	return m, cmd
}

func (m *rootModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("adm") + "\n\n")
	if m.motd != "" {
		b.WriteString(m.motd + "\n\n")
	}
	if m.errMsg != "" {
		b.WriteString(errStyle.Render(m.errMsg) + "\n\n")
	}
	switch m.phase {
	case phaseLogin:
		b.WriteString(hintStyle.Render("Login (Tab: fields, Enter: next field / submit)") + "\n\n")
		b.WriteString(m.userIn.View() + "\n")
		b.WriteString(m.passIn.View() + "\n")
	case phaseSession:
		b.WriteString(m.sessList.View())
	}
	return b.String()
}
