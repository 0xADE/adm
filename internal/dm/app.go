package dm

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const (
	builtinCmdHelpString = `
Available commands:
  :help, :?			print this help
  :poweroff, :shutdown		process poweroff command
  :reboot			process reboot command
  :suspend, :zzz		process suspend command
`
	terminalHelpString = `Usage: adm [options]
Options:
  -h, --help			print this help
  -v, --version			print version
  -d, --daemon			start in daemon mode
  -c, --config PATH		load configuration from file
  -C, --print-config	prints currently loaded configuration
  -i, --ignore-config		skips loading configuration from file
  -t, --tty NUMBER		overrides configured TTY number
  -u, --default-user USER_NAME	overrides configured default user
  -a, --autologin [SESSION]	overrides autologin; optional session name
`
)

var errPrintCommandHelp = errors.New("just print help")

// SessionHandle groups an active GUI session with auth for signal handling.
type SessionHandle struct {
	session     *commonSession
	auth        authHandle
	interrupted bool
}

// InitSessionHandle registers interrupt handling for the current login/session lifecycle.
func InitSessionHandle() *SessionHandle {
	h := &SessionHandle{}
	c := make(chan os.Signal, 10)
	signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	go handleInterrupt(c, h)
	return h
}

func handleInterrupt(c chan os.Signal, h *SessionHandle) {
	<-c
	logPrint("Caught interrupt signal")
	if err := setTerminalEcho(os.Stdout.Fd(), true); err != nil {
		logPrint(err)
	}
	h.interrupted = true
	if h.session != nil && h.session.cmd != nil {
		h.session.interrupted = true
		if err := h.session.cmd.Process.Signal(os.Interrupt); err != nil {
			logPrint("Application not responding to signal")
		} else {
			_ = h.session.cmd.Wait()
		}
	}
	if h.auth != nil {
		h.auth.CloseAuth()
	}
	os.Exit(1)
}

// ProcessCoreArgs handles -h / -v before configuration is loaded.
func ProcessCoreArgs(args []string) {
	if contains(args, "-h", "--help") {
		printHelp()
		os.Exit(0)
	}
	if contains(args, "-v", "--version") {
		fmt.Printf("adm %s\nhttps://github.com/0xADE/adm\n\nReleased under the GNU General Public License v3.\n", getVersion())
		os.Exit(0)
	}
}

// LoadConfigPath resolves config file path from argv.
func LoadConfigPath(args []string) (configPath string) {
	configPath = pathConfigFile
	for i, arg := range args {
		switch arg {
		case "-c", "--config":
			nextArg(args, i, func(val string) {
				if fileExists(val) {
					configPath = val
				}
			})
			return configPath
		case "-i", "--ignore-config":
			return ""
		}
	}
	return configPath
}

// ProcessArgs applies CLI flags to an already-allocated config.
func ProcessArgs(args []string, conf *config) {
	printConfig := false
	for i, arg := range args {
		switch arg {
		case "-t", "--tty":
			nextArg(args, i, func(val string) {
				tty := parseTTY(val, "0")
				if tty > 0 {
					conf.Tty = tty
				} else {
					ttynum := strings.SplitAfterN(val, "tty", 2)
					if len(ttynum) == 2 {
						t := parseTTY(ttynum[1], "0")
						if t > 0 {
							conf.Tty = t
						}
					}
				}
			})
		case "-u", "--default-user":
			nextArg(args, i, func(val string) {
				conf.DefaultUser = val
			})
		case "-d", "--daemon":
			conf.DaemonMode = true
		case "-a", "--autologin":
			conf.Autologin = true
			nextArg(args, i, func(val string) {
				conf.AutologinSession = val
			})
		case "-C", "--print-config":
			printConfig = true
		}
	}
	if printConfig {
		conf.printConfig()
		os.Exit(0)
	}
}

func printHelp() {
	fmt.Print(terminalHelpString)
}

func nextArg(args []string, i int, callback func(value string)) {
	if callback != nil && len(args) > i+1 {
		val := sanitizeValue(args[i+1], "")
		if !strings.HasPrefix(val, "-") {
			callback(args[i+1])
		}
	}
}

// FormatCommand strips ANSI and leading ':' from a login-buffer command.
func FormatCommand(command string) string {
	s := strings.ReplaceAll(command, "\x1b", "")
	if len(s) < 2 {
		return s
	}
	return s[1:]
}

// ShouldProcessCommand reports whether the input is a built-in :command.
func ShouldProcessCommand(input string, conf *config) bool {
	return conf.AllowCommands && strings.HasPrefix(strings.ReplaceAll(input, "\x1b", ""), ":")
}

// ProcessCommand runs power/session-wide commands (poweroff, reboot, suspend, help).
func ProcessCommand(command string, c *config, auth authHandle, continuable bool) error {
	switch command {
	case "help", "?":
		fmt.Print(builtinCmdHelpString)
		if continuable {
			return errPrintCommandHelp
		}
		waitForReturnToExit(0)
	case "poweroff", "shutdown":
		if continuable && auth != nil {
			auth.CloseAuth()
		}
		if err := processCommandAsCmd(c.CmdPoweroff); err == nil {
			waitForReturnToExit(0)
		} else {
			handleErr(err)
		}
	case "reboot":
		if continuable && auth != nil {
			auth.CloseAuth()
		}
		if err := processCommandAsCmd(c.CmdReboot); err == nil {
			waitForReturnToExit(0)
		} else {
			handleErr(err)
		}
	case "suspend", "zzz":
		if continuable && auth != nil {
			auth.CloseAuth()
		}
		variants := []string{
			"zzz",
			"systemctl suspend",
			"loginctl suspend",
		}
		if c.CmdSuspend != "" {
			variants = append([]string{c.CmdSuspend}, variants...)
		}
		var err error
		for _, v := range variants {
			if err = processCommandAsCmd(v); err != nil {
				continue
			}
			break
		}
		if err == nil {
			waitForReturnToExit(0)
		} else {
			handleErr(err)
		}
	default:
		err := fmt.Errorf("unknown command '%s'", command)
		if continuable {
			return err
		}
		handleErr(err)
	}
	return nil
}
