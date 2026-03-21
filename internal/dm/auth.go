package dm

import (
	"os"
	"strings"
)

const (
	pathLastLoggedInUser = "/var/cache/adm/lastuser"

	constEnSelectLastUserFalse  = "false"
	constEnSelectLastUserPerTTy = "per-tty"
	constEnSelectLastUserGlobal = "global"
)

type enSelectLastUser byte

const (
	// Do not preselect any last user
	False enSelectLastUser = iota

	// Preselect last successfully logged in user per tty
	PerTty

	// Preselect last successfully logged in user per system
	Global
)

type authBase struct {
	command string
}

func (a *authBase) GetCommand() string {
	if a == nil {
		return ""
	}
	return a.command
}

// SetCommandFromUsername detects :commands in the username field.
func (a *authBase) SetCommandFromUsername(username string, conf *config) bool {
	u := strings.TrimSpace(username)
	if conf.AllowCommands && ShouldProcessCommand(u, conf) {
		a.command = FormatCommand(u)
		return true
	}
	return false
}

// LastUserHint returns the cached last login name (per SELECT_LAST_USER), if any.
func LastUserHint(conf *Config) string {
	a := &authBase{}
	return a.getLastSelectedUser(conf)
}

// EffectiveUsername applies DefaultUser and last-logged hint when input is empty.
func EffectiveUsername(conf *config, username string) string {
	u := strings.TrimSpace(username)
	if u != "" {
		return u
	}
	if conf.DefaultUser != "" {
		return conf.DefaultUser
	}
	a := &authBase{}
	if last := a.getLastSelectedUser(conf); last != "" {
		return last
	}
	return ""
}

// Gets last selected user with respect to configuration.
func (a *authBase) getLastSelectedUser(c *config) string {
	switch c.SelectLastUser {
	case PerTty:
		return a.readLastUser(pathLastLoggedInUser + "-" + c.strTTY())
	case Global:
		return a.readLastUser(pathLastLoggedInUser)
	}
	return ""
}

// Reads last user from file on path.
func (a *authBase) readLastUser(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logPrint(err)
		}
		return ""
	}
	lastUser := strings.TrimSpace(string(b))
	if strings.Contains(lastUser, "\n") {
		return ""
	}
	return lastUser
}

// Saves last selected user with respect to configuration.
func (a *authBase) saveLastSelectedUser(c *config, username string) {
	if c.SelectLastUser == False {
		return
	}

	path := pathLastLoggedInUser
	if c.SelectLastUser == PerTty {
		path += "-" + c.strTTY()
	}

	if err := mkDirsForFile(path, 0700); err != nil {
		logPrint(err)
	}
	if err := os.WriteFile(path, []byte(username), 0600); err != nil {
		logPrint(err)
	}
}
