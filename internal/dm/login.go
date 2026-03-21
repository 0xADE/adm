package dm

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
)

const (
	loginRetryFileBase = "/tmp/adm/login-retry-"
)

// authHandle defines authorization state for a session.
type authHandle interface {
	usr() *sysuser
	CloseAuth()
	defineSpecificEnvVariables()
	openAuthSession(string) error
	GetCommand() string
}

// AuthHandle is implemented by PAM and nopam backends.
type AuthHandle = authHandle

// UserOf returns the authenticated account, or nil.
// It is safe when the interface holds a typed-nil concrete pointer (e.g. (*pamHandle)(nil)).
func UserOf(a AuthHandle) *Sysuser {
	if a == nil {
		return nil
	}
	rv := reflect.ValueOf(a)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		if rv.IsNil() {
			return nil
		}
	}
	return a.usr()
}

// RunLoginSession starts the graphical session after auth and desktop resolution.
func RunLoginSession(conf *config, h *SessionHandle, auth authHandle, d *desktop) string {
	if auth != nil && auth.GetCommand() != "" {
		return auth.GetCommand()
	}

	h.auth = auth
	defer func() { h.auth = nil }()

	if err := handleLoginRetries(conf, &DefaultLoginRetryPathProvider{}); err != nil {
		auth.CloseAuth()
		handleStrErr("Exceeded maximum number of allowed login retries in short period.")
		return ""
	}

	if h.interrupted {
		return ""
	}

	runDisplayScript(conf.DisplayStartScript)

	if err := auth.openAuthSession(d.env.sessionType()); err != nil {
		auth.CloseAuth()
		handleStrErr("No active transaction")
		return ""
	}

	h.session = createSession(auth, d, conf)
	h.session.start()

	auth.CloseAuth()

	runDisplayScript(conf.DisplayStopScript)

	return ""
}

// LoadUserConfig reads ~/.config/adm or ~/.adm and optional LANG for the user.
func LoadUserConfig(homeDir string) (ud *Desktop, usrLang string) {
	d, lang := loadUserDesktop(homeDir)
	return d, lang
}

// TryAutoSelectDesktop returns a desktop without UI when autologin/default/single-session rules apply.
func TryAutoSelectDesktop(auth authHandle, conf *config, ud *desktop) (chosen *desktop, desktops []*desktop, lastIdx int, needUI bool) {
	usr := auth.usr()
	desktops = listAllDesktops(usr, conf.XorgSessionsPath, conf.WaylandSessionsPath)
	if len(desktops) == 0 {
		handleStrErr("Not found any installed desktop.")
	}
	lastIdx = getLastDesktop(usr, desktops)
	allowAutoselect := ud == nil || ud.selection == SelectionFalse

	if conf.Autologin && conf.AutologinSession != "" {
		if d := findAutoselectDesktop(conf.AutologinSession, conf.AutologinSessionEnv, desktops); d != nil {
			return d, desktops, lastIdx, false
		}
	}
	if conf.DefaultSession != "" && allowAutoselect {
		if d := findAutoselectDesktop(conf.DefaultSession, conf.DefaultSessionEnv, desktops); d != nil {
			return d, desktops, lastIdx, false
		}
	}
	if len(desktops) == 1 && (conf.AutoSelection || (ud != nil && ud.selection == SelectionAuto)) {
		return desktops[0], desktops, lastIdx, false
	}
	return nil, desktops, lastIdx, true
}

// FinalizeDesktopSelection merges user config, selection, and last-session persistence.
func FinalizeDesktopSelection(auth authHandle, conf *config, ud *desktop, selected *desktop, desktops []*desktop) *desktop {
	usr := auth.usr()
	_, usrLang := loadUserDesktop(usr.homedir)
	if usrLang != "" {
		conf.UserLang = usrLang
	}
	if ud != nil && ud.selection == SelectionFalse {
		return ud
	}
	lastIdx := getLastDesktop(usr, desktops)
	var lastRef *desktop
	if lastIdx >= 0 && lastIdx < len(desktops) {
		lastRef = desktops[lastIdx]
	}
	if lastRef != nil && isLastDesktopForSave(usr, lastRef, selected) {
		setUserLastSession(usr, selected)
	}
	if ud != nil && ud.selection != SelectionFalse {
		ud.child = selected
		ud.env = ud.child.env
		return ud
	}
	return selected
}

// Runs display script, if defined
func runDisplayScript(scriptPath string) {
	if scriptPath != "" {
		if fileIsExecutable(scriptPath) {
			if err := exec.Command(scriptPath).Run(); err != nil {
				logPrint(err)
			}
		} else {
			logPrint(scriptPath + " is not executable.")
		}
	}
}

// Handles keeping information about last login with retry.
func handleLoginRetries(conf *config, retryProvider LoginRetryPathProvider) (result error) {
	// infinite allowed retries, return to avoid writing into file
	if conf.AutologinMaxRetry < 0 {
		return nil
	}

	if conf.AutologinMaxRetry >= 0 {
		retriesPath := retryProvider.getLoginRetryPath(conf)
		retries, lastTime := readRetryFile(retriesPath)

		// Check if last retry was within last X seconds defined by AutologinRetryPeriod
		currTime := getUptime()
		limit := currTime - float64(conf.AutologinRtryPeriod)
		if lastTime >= limit {
			retries++

			if retries >= conf.AutologinMaxRetry {
				result = errors.New("exceeded maximum number of allowed login retries in short period")
				retries = 0
			}
		}
		writeRetryFile(retriesPath, retries, currTime)
	}

	return result
}

// Parse the retry file at the given path and return time and retry count
func readRetryFile(path string) (retries int, time float64) {
	content, err := os.ReadFile(path)
	if err != nil {
		return retries, time
	}
	contentSlice := strings.Split(string(content), ":")
	contentSliceLen := len(contentSlice)

	if contentSliceLen > 0 && contentSliceLen <= 2 {
		retries, _ = strconv.Atoi(strings.TrimSpace(contentSlice[0]))
		if contentSliceLen == 2 {
			time, _ = strconv.ParseFloat(strings.TrimSpace(contentSlice[1]), 64)
		}
	} else {
		logPrint("Unable to parse the user login retry file")
	}

	return retries, time
}

// Write the given retry count and time to a file at the given path
func writeRetryFile(path string, retries int, time float64) {
	if err := mkDirsForFile(path, 0700); err != nil {
		logPrint(err)
	}

	result := []byte(strconv.Itoa(retries) + ":")
	result = strconv.AppendFloat(result, time, 'f', -1, 64)
	if err := os.WriteFile(path, result, 0600); err != nil {
		logPrint(err)
	}
}

// Attempt to fetch the current device uptime
func getUptime() (uptime float64) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		logPrint("Unable to read /proc/uptime")
		return 0
	}

	slice := strings.Split(string(data), " ")
	uptime, err = strconv.ParseFloat(slice[0], 64)
	if err != nil {
		logPrint("Unable to parse uptime value")
		return 0
	}

	return uptime
}

type LoginRetryPathProvider interface {
	getLoginRetryPath(conf *config) string
}

type DefaultLoginRetryPathProvider struct {
}

// Return a tty specific retry file path, future proofing for multi-seat
func (r *DefaultLoginRetryPathProvider) getLoginRetryPath(conf *config) string {
	return loginRetryFileBase + conf.strTTY()
}
