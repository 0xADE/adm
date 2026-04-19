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

	menuItemStyle    = lipgloss.NewStyle().Padding(0, 1)
	menuItemSelStyle = lipgloss.NewStyle().Padding(0, 1).Reverse(true).Bold(true)
)

type phase int

const (
	phaseLogin phase = iota
	phaseSession
	phaseSessionError
)

type deskItem struct {
	d *dm.Desktop
}

// cmdMenuItem is one entry in the bottom command menu shown on every TUI
// screen. It maps the user-facing label to the dm.ProcessCommand verb
// (poweroff/reboot/suspend) that should be executed when the item is
// activated via Tab + Enter.
type cmdMenuItem struct {
	label   string
	command string
}

// buildCmdMenu returns the bottom command menu derived from the active
// configuration. The menu is empty when commands are disabled globally
// (AllowCommands=false) and individual entries are omitted when the
// corresponding configuration field is blank, mirroring the behaviour of
// dm.ProcessCommand which treats blank commands as a no-op.
func buildCmdMenu(c *dm.Config) []cmdMenuItem {
	if !c.AllowCommands {
		return nil
	}
	var m []cmdMenuItem
	if strings.TrimSpace(c.CmdReboot) != "" {
		m = append(m, cmdMenuItem{label: "Reboot", command: "reboot"})
	}
	if strings.TrimSpace(c.CmdPoweroff) != "" {
		m = append(m, cmdMenuItem{label: "Shutdown", command: "poweroff"})
	}
	if strings.TrimSpace(c.CmdSuspend) != "" {
		m = append(m, cmdMenuItem{label: "Suspend", command: "suspend"})
	}
	return m
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

	// Cached credentials kept in memory between session-launch attempts so the
	// user does not have to re-type them after a failed environment start.
	// Cleared when control returns to phaseLogin or when the program exits.
	username   string
	password   string
	sessionErr string

	// Bottom command menu shown on every TUI phase. Built once in NewRoot;
	// nil/empty when commands are disabled or no commands are configured.
	cmdMenu []cmdMenuItem
	// Per-phase selection inside cmdMenu. -1 means focus is on the phase's
	// primary widget (login fields, session list, error retry); 0..N-1 means
	// focus is on that menu item.
	loginMenuIdx int
	sessMenuIdx  int
	errMenuIdx   int

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
		conf:         conf,
		motd:         motd,
		ver:          version,
		h:            h,
		userIn:       tu,
		passIn:       tp,
		phase:        phaseLogin,
		asciiCursor:  asciiCursor,
		cmdMenu:      buildCmdMenu(conf),
		loginMenuIdx: -1,
		sessMenuIdx:  -1,
		errMenuIdx:   -1,
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
	case phaseSessionError:
		return m.updateSessionError(msg)
	}
	return m, nil
}

// advanceLoginFocus moves focus around the login phase's circular ring
// of (user field, pass field, menu item 0, ..., menu item N-1). delta is
// +1 for Tab and -1 for Shift+Tab. Field cursors are activated/deactivated
// to match the new focus; the menu has no cursor of its own (highlight is
// rendered by View() based on m.loginMenuIdx).
func (m *rootModel) advanceLoginFocus(delta int) tea.Cmd {
	n := 2 + len(m.cmdMenu)
	cur := m.focus
	if m.loginMenuIdx >= 0 {
		cur = 2 + m.loginMenuIdx
	}
	cur = ((cur+delta)%n + n) % n
	if cur < 2 {
		m.loginMenuIdx = -1
		m.focus = cur
		if cur == 0 {
			m.passIn.Blur()
			setLoginCursorInactive(&m.passIn)
			m.userIn.Focus()
			setLoginCursorActive(&m.userIn)
		} else {
			m.userIn.Blur()
			setLoginCursorInactive(&m.userIn)
			m.passIn.Focus()
			setLoginCursorActive(&m.passIn)
		}
		return textinput.Blink
	}
	m.loginMenuIdx = cur - 2
	m.userIn.Blur()
	setLoginCursorInactive(&m.userIn)
	m.passIn.Blur()
	setLoginCursorInactive(&m.passIn)
	return nil
}

func (m *rootModel) updateLogin(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			return m, m.advanceLoginFocus(+1)
		case "shift+tab":
			return m, m.advanceLoginFocus(-1)
		case "enter":
			if m.loginMenuIdx >= 0 {
				cmd := m.cmdMenu[m.loginMenuIdx].command
				_ = dm.ProcessCommand(cmd, m.conf, nil, false)
				return m, tea.Quit
			}
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
			if cmd := auth.GetCommand(); cmd != "" {
				_ = dm.ProcessCommand(cmd, m.conf, auth, false)
				auth.CloseAuth()
				return m, tea.Quit
			}
			m.errMsg = ""
			m.auth = auth
			m.username = strings.TrimSpace(user)
			m.password = pass

			su := dm.UserOf(auth)
			if su == nil {
				m.errMsg = "no user record after authentication (internal error)"
				return m, nil
			}
			ud, _ := dm.LoadUserConfig(su.HomeDir())
			chosen, desktops, lastIdx, needUI := dm.TryAutoSelectDesktop(auth, m.conf, ud)
			m.desktops = desktops
			m.lastIdx = lastIdx
			m.buildSessionList(desktops, lastIdx)

			if ud != nil && ud.SelectionMode() == dm.SelectionFalse {
				d := dm.FinalizeDesktopSelection(auth, m.conf, ud, ud, desktops)
				return m.runSessionAndContinue(auth, d, d.Name())
			}
			if !needUI && chosen != nil {
				d := dm.FinalizeDesktopSelection(auth, m.conf, ud, chosen, desktops)
				return m.runSessionAndContinue(auth, d, chosen.Name())
			}

			m.phase = phaseSession
			return m, nil
		}
	}

	if m.loginMenuIdx >= 0 {
		return m, nil
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.userIn, cmd = m.userIn.Update(msg)
	} else {
		m.passIn, cmd = m.passIn.Update(msg)
	}
	return m, cmd
}

// renderCmdMenu renders the bottom command menu as a single horizontal row
// of bracketed labels. The item at index sel (0..len(items)-1) is rendered
// in the selected style; sel < 0 renders all items in the unselected style
// (e.g. when focus is on the primary widget of the current phase). Returns
// an empty string when there are no items, so callers can distinguish "no
// menu at all" from "menu with nothing focused".
func renderCmdMenu(items []cmdMenuItem, sel int) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, len(items))
	for i, it := range items {
		label := "[ " + it.label + " ]"
		if i == sel {
			parts[i] = menuItemSelStyle.Render(label)
		} else {
			parts[i] = menuItemStyle.Render(label)
		}
	}
	return strings.Join(parts, "  ")
}

// nextMenuIdx advances a per-phase menu selection by delta (+1 or -1) and
// wraps around the sentinel value -1 (which represents "focus on the
// primary widget of the phase"). The cycle is therefore -1, 0, 1, ...,
// n-1, -1, 0, ..., independent of the caller.
func nextMenuIdx(cur, n, delta int) int {
	if n <= 0 {
		return -1
	}
	total := n + 1
	v := (cur + 1 + delta) % total
	if v < 0 {
		v += total
	}
	return v - 1
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
		switch msg.String() {
		case "tab":
			if len(m.cmdMenu) == 0 {
				return m, nil
			}
			m.sessMenuIdx = nextMenuIdx(m.sessMenuIdx, len(m.cmdMenu), +1)
			return m, nil
		case "shift+tab":
			if len(m.cmdMenu) == 0 {
				return m, nil
			}
			m.sessMenuIdx = nextMenuIdx(m.sessMenuIdx, len(m.cmdMenu), -1)
			return m, nil
		case "enter":
			if m.sessMenuIdx >= 0 {
				cmd := m.cmdMenu[m.sessMenuIdx].command
				_ = dm.ProcessCommand(cmd, m.conf, nil, false)
				return m, tea.Quit
			}
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
			return m.runSessionAndContinue(auth, d, selected.Name())
		}
		if m.sessMenuIdx >= 0 {
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.sessList, cmd = m.sessList.Update(msg)
	return m, cmd
}

// buildSessionList constructs the session-selection list for the given desktops.
// It is called both on the initial transition into phaseSession and whenever the
// list needs to be (re)built after an autoselect path or a failed launch.
func (m *rootModel) buildSessionList(desktops []*dm.Desktop, lastIdx int) {
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
}

// runSessionAndContinue invokes dm.RunLoginSession and decides how to proceed.
// On a clean session end (or built-in :command path) the program quits.
// On a failed environment start the user is taken to phaseSessionError where a
// short message is shown and Enter returns them to the session list (after a
// silent re-authentication using the cached username/password).
func (m *rootModel) runSessionAndContinue(auth dm.AuthHandle, d *dm.Desktop, label string) (tea.Model, tea.Cmd) {
	_, err := dm.RunLoginSession(m.conf, m.h, auth, d)
	m.auth = nil
	if err != nil {
		if label != "" {
			m.sessionErr = "Failed to start " + label + ": " + err.Error()
		} else {
			m.sessionErr = "Failed to start session: " + err.Error()
		}
		m.phase = phaseSessionError
		return m, tea.ClearScreen
	}
	return m, tea.Quit
}

// updateSessionError handles the modal error screen shown when the chosen
// environment failed to start. Pressing Enter re-authenticates the user with
// the cached credentials and returns to phaseSession; if re-auth fails (rare:
// PAM policy change between attempts) the user is dropped back to phaseLogin
// with an explanatory message and the password field cleared.
func (m *rootModel) updateSessionError(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termW, m.termH = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "tab":
			if len(m.cmdMenu) == 0 {
				return m, nil
			}
			m.errMenuIdx = nextMenuIdx(m.errMenuIdx, len(m.cmdMenu), +1)
			return m, nil
		case "shift+tab":
			if len(m.cmdMenu) == 0 {
				return m, nil
			}
			m.errMenuIdx = nextMenuIdx(m.errMenuIdx, len(m.cmdMenu), -1)
			return m, nil
		case "enter":
			if m.errMenuIdx >= 0 {
				cmd := m.cmdMenu[m.errMenuIdx].command
				_ = dm.ProcessCommand(cmd, m.conf, nil, false)
				return m, tea.Quit
			}
			auth, err := dm.Authenticate(m.conf, m.username, m.password)
			if err != nil {
				m.phase = phaseLogin
				m.sessionErr = ""
				m.errMsg = err.Error()
				m.passIn.SetValue("")
				m.password = ""
				m.focus = 1
				m.userIn.Blur()
				setLoginCursorInactive(&m.userIn)
				setLoginCursorActive(&m.passIn)
				m.passIn.Focus()
				return m, textinput.Blink
			}
			m.auth = auth
			m.sessionErr = ""
			m.phase = phaseSession
			return m, nil
		}
	}
	return m, nil
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
		b.WriteString(hintStyle.Render("Login (Tab: fields/commands, Enter: submit)") + "\n\n")
		b.WriteString(renderLoginInput(m.userIn, m.asciiCursor) + "\n")
		b.WriteString(renderLoginInput(m.passIn, m.asciiCursor) + "\n")
	case phaseSession:
		b.WriteString(m.sessList.View())
	case phaseSessionError:
		b.WriteString(errStyle.Render(m.sessionErr) + "\n\n")
		b.WriteString(hintStyle.Render("Press Enter to return to session selection..."))
	}
	b.WriteString("\n\n")
	b.WriteString(m.ver)
	inner := b.String()

	sel := -1
	switch m.phase {
	case phaseLogin:
		sel = m.loginMenuIdx
	case phaseSession:
		sel = m.sessMenuIdx
	case phaseSessionError:
		sel = m.errMenuIdx
	}
	menu := renderCmdMenu(m.cmdMenu, sel)

	if m.termW > 0 && m.termH > 0 {
		if menu == "" {
			return lipgloss.Place(m.termW, m.termH, lipgloss.Center, lipgloss.Center, inner)
		}
		bottomH := lipgloss.Height(menu) + 1
		topH := m.termH - bottomH
		if topH < 1 {
			topH = 1
		}
		top := lipgloss.Place(m.termW, topH, lipgloss.Center, lipgloss.Center, inner)
		bottom := lipgloss.Place(m.termW, bottomH, lipgloss.Center, lipgloss.Bottom, menu)
		return lipgloss.JoinVertical(lipgloss.Left, top, bottom)
	}
	if menu != "" {
		return inner + "\n" + menu
	}
	return inner
}
