//go:build !nopam

package dm

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/msteinert/pam/v2"
)

const tagPam = ""

type pamState byte

const (
	pamInit pamState = iota
	pamAuthenticated
	pamCredsEstablished
	pamSessionOpened
	pamClosed
)

// PamHandle is the PAM-backed auth handle (service name "adm").
type pamHandle struct {
	*authBase
	trans *pam.Transaction
	u     *sysuser
	pamState
}

// AuthenticatePAM validates credentials via PAM. On success the handle is ready for openAuthSession / session start.
// If the username is a :command (and AllowCommands), no PAM transaction is opened; getCommand is set instead.
func AuthenticatePAM(conf *config, username, password string) (*pamHandle, error) {
	h := &pamHandle{authBase: &authBase{}}
	if err := h.authenticate(conf, username, password); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *pamHandle) authenticate(conf *config, username, password string) error {
	u := strings.TrimSpace(username)
	if h.SetCommandFromUsername(u, conf) {
		return nil
	}
	eff := EffectiveUsername(conf, u)
	if eff == "" {
		return errors.New("no username")
	}

	h.pamState = pamInit
	var err error
	h.trans, err = pam.StartFunc("adm", eff, func(s pam.Style, msg string) (string, error) {
		switch s {
		case pam.PromptEchoOff:
			if conf.Autologin {
				return "", nil
			}
			return password, nil
		case pam.PromptEchoOn:
			return "", nil
		case pam.ErrorMsg:
			logPrint(msg)
			return "", nil
		case pam.TextInfo:
			logPrint(msg)
			return "", nil
		}
		return "", errors.New("unrecognized PAM message style")
	})
	if err != nil {
		return err
	}

	if err := h.trans.Authenticate(pam.DisallowNullAuthtok); err != nil {
		pamUser, _ := h.trans.GetItem(pam.User)
		addBtmpEntry(pamUser, os.Getpid(), conf.strTTY())
		h.CloseAuth()
		return err
	}
	h.pamState = pamAuthenticated
	logPrint("Authenticate OK")

	if err := h.trans.AcctMgmt(pam.Silent); err != nil {
		h.CloseAuth()
		return err
	}
	if err := h.trans.SetItem(pam.Tty, "tty"+conf.strTTY()); err != nil {
		h.CloseAuth()
		return err
	}
	if err := h.trans.SetCred(pam.EstablishCred); err != nil {
		h.CloseAuth()
		return err
	}
	h.pamState = pamCredsEstablished

	pamUsr, _ := h.trans.GetItem(pam.User)
	usr, err := user.Lookup(pamUsr)
	if err != nil {
		h.CloseAuth()
		return err
	}

	h.u = getSysuser(usr)
	h.saveLastSelectedUser(conf, pamUsr)
	return nil
}

func (h *pamHandle) usr() *sysuser {
	return h.u
}

func (h *pamHandle) CloseAuth() {
	if h != nil && h.trans != nil && h.pamState < pamClosed {
		logPrint("Closing PAM auth")
		defer func() {
			if err := h.trans.End(); err != nil {
				logPrint(err)
			}
			h.trans = nil
			h.u = nil
			h.pamState = pamClosed
		}()

		if h.pamState >= pamSessionOpened {
			if err := h.trans.CloseSession(pam.Silent); err != nil {
				logPrint(err)
			}
		}

		if h.pamState >= pamCredsEstablished {
			if err := h.trans.SetCred(pam.DeleteCred); err != nil {
				logPrint(err)
			}
		}
	}
}

func (h *pamHandle) defineSpecificEnvVariables() {
	if h.trans != nil && h.u != nil {
		envs, _ := h.trans.GetEnvList()
		for key, value := range envs {
			h.u.setenv(key, value)
		}
	}
}

func (h *pamHandle) openAuthSession(sessionType string) error {
	if h.trans != nil {
		if err := h.trans.PutEnv(fmt.Sprintf("XDG_SESSION_TYPE=%s", sessionType)); err != nil {
			return err
		}
		if err := h.trans.OpenSession(pam.Silent); err != nil {
			return err
		}
		h.pamState = pamSessionOpened
		return nil
	}
	return errors.New("no active transaction")
}
