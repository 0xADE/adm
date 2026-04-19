package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/0xADE/adm/internal/dm"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("62"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	// High-contrast block for textinput cursor (works when reverse video is weak or off).
	cursorFieldStyle = lipgloss.NewStyle().Background(lipgloss.Color("250")).Foreground(lipgloss.Color("0"))
	// Blurred textinput still renders Cursor.View(); empty styles avoid a leftover block.
	cursorBlurredStyle = lipgloss.NewStyle()
)

type phase int

const (
	phaseLogin phase = iota
	phaseSession
)

type deskItem struct {
	d *dm.Desktop
}

// sessionEnvColumn is the visual column (0-based end of padding) before ":x11" / ":wayland".
const sessionEnvColumn = 24

func (i deskItem) Title() string {
	return formatSessionLine(i.d.Name(), i.d.EnvLabel())
}

func (i deskItem) Description() string { return "" }

func (i deskItem) FilterValue() string {
	return i.d.Name() + " " + i.d.EnvLabel()
}

func formatSessionLine(name, env string) string {
	nw := ansi.StringWidth(name)
	pad := sessionEnvColumn - nw
	if pad < 1 {
		pad = 1
	}
	return name + strings.Repeat(" ", pad) + ":" + env
}

// rootModel drives login and session selection in one program.
type rootModel struct {
	conf   *dm.Config
	motd   string
	ver    string
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

	termW int
	termH int

	// asciiCursor is true when the terminal does not support 256 colours or
	// better.  In that mode the standard colour-block cursor is invisible on
	// blank fields, so we render an explicit '_' glyph instead.
	asciiCursor bool
}

func setLoginCursorActive(ti *textinput.Model) {
	ti.Cursor.Style = cursorFieldStyle
	ti.Cursor.TextStyle = cursorFieldStyle
}

func setLoginCursorInactive(ti *textinput.Model) {
	ti.Cursor.Style = cursorBlurredStyle
	ti.Cursor.TextStyle = cursorBlurredStyle
}

// NewRoot creates the Bubble Tea model for one login + session cycle.
func NewRoot(conf *dm.Config, motd, version string, h *dm.SessionHandle) tea.Model {
	prof := colorprofile.Detect(os.Stdout, os.Environ())
	asciiCursor := prof < colorprofile.ANSI256
	tu := textinput.New()
	tu.Prompt = "user > "
	tu.Placeholder = ""
	tu.CharLimit = 256
	tu.Width = 40
	tu.Cursor.Style = cursorFieldStyle
	tu.Cursor.TextStyle = cursorFieldStyle
	tu.Focus()

	tp := textinput.New()
	tp.Prompt = "pass > "
	tp.Placeholder = ""
	tp.EchoMode = textinput.EchoPassword
	tp.CharLimit = 256
	tp.Width = 40
	tp.Cursor.Style = cursorFieldStyle
	tp.Cursor.TextStyle = cursorFieldStyle
	setLoginCursorInactive(&tp)

	if conf.DefaultUser != "" {
		tu.SetValue(conf.DefaultUser)
	} else if last := dm.LastUserHint(conf); last != "" {
		tu.SetValue(last)
	}

	return &rootModel{
		conf:        conf,
		motd:        motd,
		ver:         version,
		h:           h,
		userIn:      tu,
		passIn:      tp,
		phase:       phaseLogin,
		asciiCursor: asciiCursor,
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
		m.termW, m.termH = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab":
			if m.focus == 0 {
				m.focus = 1
				m.userIn.Blur()
				setLoginCursorInactive(&m.userIn)
				setLoginCursorActive(&m.passIn)
				m.passIn.Focus()
			} else {
				m.focus = 0
				m.passIn.Blur()
				setLoginCursorInactive(&m.passIn)
				setLoginCursorActive(&m.userIn)
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
				setLoginCursorInactive(&m.userIn)
				setLoginCursorActive(&m.passIn)
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
			delegate.ShowDescription = false
			delegate.SetSpacing(0)

			listW := sessionListWidth(m.termW)
			listH := min(22, len(items)+6)
			if m.termH > 0 {
				listH = m.termH - 10
				if listH < 6 {
					listH = 6
				}
			}
			l := list.New(items, delegate, listW, listH)
			l.Title = "Select display environment"
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// sessionListWidth caps list width so the session panel can sit centered in wide terminals.
func sessionListWidth(termW int) int {
	if termW <= 0 {
		return 80
	}
	return min(80, max(40, termW-8))
}

func (m *rootModel) updateSession(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
		m.sessList.SetWidth(sessionListWidth(msg.Width))
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

// renderLoginInput renders a textinput field.  When asciiCursor is true the
// terminal is assumed to lack 256-colour support, so on the plain-colour
// palette a coloured block cursor is invisible over a blank character.  In
// that case, when the cursor sits at the end of the value (the most common
// situation on an empty field or after typing), we manually append a '_'
// glyph that remains visible even without colour.  The glyph is drawn with
// reverse video so it is also highlighted on terminals that do support that
// attribute.  Blinking is preserved by replacing the glyph with a space when
// ti.Cursor.Blink is true (cursor currently in its hidden phase).
//
// For all other cursor positions, and for colour terminals, the standard
// ti.View() output is used unchanged.
func renderLoginInput(ti textinput.Model, asciiCursor bool) string {
	if !asciiCursor || !ti.Focused() {
		return ti.View()
	}

	val := ti.Value()
	pos := ti.Position()
	if pos < len([]rune(val)) {
		// Cursor is inside existing text: standard rendering is fine because
		// the character under the cursor is already a visible glyph.
		return ti.View()
	}

	// Cursor is at end of value (including empty field).  Build the visible
	// portion of the value respecting EchoPassword and Width.
	var displayVal string
	switch ti.EchoMode {
	case textinput.EchoPassword:
		displayVal = strings.Repeat(string(ti.EchoCharacter), uniseg.StringWidth(val))
	default:
		displayVal = val
	}

	// Truncate to visible width when Width is set (mirror textinput viewport).
	if ti.Width > 0 {
		displayVal = ansi.Truncate(displayVal, ti.Width, "")
	}

	var cursorGlyph string
	if ti.Cursor.Blink {
		cursorGlyph = " "
	} else {
		cursorGlyph = lipgloss.NewStyle().Reverse(true).Render("_")
	}

	return ti.PromptStyle.Render(ti.Prompt) + ti.TextStyle.Inline(true).Render(displayVal) + cursorGlyph
}

func (m *rootModel) View() string {
	var b strings.Builder
	if m.motd != "" {
		b.WriteString(m.motd + "\n\n")
	}
	if m.errMsg != "" {
		b.WriteString(errStyle.Render(m.errMsg) + "\n\n")
	}
	switch m.phase {
	case phaseLogin:
		b.WriteString(hintStyle.Render("Login (Tab: fields, Enter: next field / submit)") + "\n\n")
		b.WriteString(renderLoginInput(m.userIn, m.asciiCursor) + "\n")
		b.WriteString(renderLoginInput(m.passIn, m.asciiCursor) + "\n")
	case phaseSession:
		b.WriteString(m.sessList.View())
	}
	b.WriteString("\n\n")
	b.WriteString(m.ver)
	inner := b.String()
	if m.termW > 0 && m.termH > 0 {
		return lipgloss.Place(m.termW, m.termH, lipgloss.Center, lipgloss.Center, inner)
	}
	return inner
}
