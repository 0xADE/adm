//go:build nopam

package dm

import (
	"errors"
	"fmt"
	"os"
	"os/user"
)

const tagPam = "nopam"

// NopamHandle performs login without PAM (build tag nopam).
type nopamHandle struct {
	*authBase
	u *sysuser
}

// AuthenticateNopam validates credentials without PAM (shadow/crypt).
func AuthenticateNopam(conf *config, username, password string) (*nopamHandle, error) {
	h := &nopamHandle{authBase: &authBase{}}
	if h.SetCommandFromUsername(username, conf) {
		return h, nil
	}
	if conf.Autologin && conf.DefaultUser != "" {
		usr, err := user.Lookup(conf.DefaultUser)
		if err != nil {
			return nil, err
		}
		h.u = getSysuser(usr)
		return h, nil
	}
	eff := EffectiveUsername(conf, username)
	if eff == "" {
		return nil, errors.New("no username")
	}
	if !h.authPassword(eff, password) {
		addBtmpEntry(eff, os.Getpid(), conf.strTTY())
		return nil, fmt.Errorf("authentication failure")
	}
	h.saveLastSelectedUser(conf, eff)
	usr, err := user.Lookup(eff)
	if err != nil {
		return nil, err
	}
	h.u = getSysuser(usr)
	return h, nil
}

func (n *nopamHandle) usr() *sysuser {
	return n.u
}

func (n *nopamHandle) CloseAuth() {}

func (n *nopamHandle) defineSpecificEnvVariables() {}

func (n *nopamHandle) openAuthSession(sessionType string) error {
	return nil
}
