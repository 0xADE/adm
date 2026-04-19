package dm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// xorgSession defines structure for xorg
type xorgSession struct {
	*commonSession
	xorg *exec.Cmd
}

// Starts Xorg as carrier for Xorg Session.
// Returns an error so the caller can show a short message and return the user
// to the session selection without terminating adm.
func (x *xorgSession) startCarrier() error {
	if !x.conf.DefaultXauthority {
		x.auth.usr().setenv(envXauthority, x.auth.usr().getenv(envXdgRuntimeDir)+"/.adm-xauth")
		os.Remove(x.auth.usr().getenv(envXauthority))
	}

	x.auth.usr().setenv(envDisplay, ":"+x.getFreeXDisplay())

	cmd := cmdAsUser(x.auth.usr(), lookPath("mcookie", "/usr/bin/mcookie"))
	mcookie, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("mcookie failed: %w", err)
	}
	logPrint("Generated mcookie")

	cmd = cmdAsUser(x.auth.usr(), lookPath("xauth", "/usr/bin/xauth"), "add", x.auth.usr().getenv(envDisplay), ".", string(mcookie))
	if _, err = cmd.Output(); err != nil {
		return fmt.Errorf("xauth failed: %w", err)
	}
	logPrint("Generated xauthority")

	logPrint("Starting X server (" + x.conf.XorgBin + ")")

	xorgBin := lookPath(x.conf.XorgBin, "/usr/bin/"+x.conf.XorgBin)
	xorgArgs := []string{"vt" + x.conf.strTTY(), x.auth.usr().getenv(envDisplay)}
	if x.allowRootlessX() {
		xorgArgs = append(xorgArgs, "-keeptty")
	}

	if x.conf.XorgArgs != "" {
		arrXorgArgs := parseExec(x.conf.XorgArgs)
		xorgArgs = append(xorgArgs, arrXorgArgs...)
	}

	if x.allowRootlessX() {
		x.xorg = cmdAsUser(x.auth.usr(), xorgBin, xorgArgs...)
		x.xorg.Env = x.auth.usr().environ()
		if err := x.setTTYOwnership(x.conf, x.auth.usr().uid); err != nil {
			logPrint(err)
		}
	} else {
		x.xorg = exec.Command(xorgBin, xorgArgs...)
		os.Setenv(envDisplay, x.auth.usr().getenv(envDisplay))
		os.Setenv(envXauthority, x.auth.usr().getenv(envXauthority))
		x.xorg.Env = os.Environ()
	}

	if err := x.xorg.Start(); err != nil {
		return fmt.Errorf("xorg start failed: %w", err)
	}
	if x.xorg.Process == nil {
		return errors.New("Xorg is not running")
	}
	logPrint("Started Xorg")

	if err := openXDisplay(x.auth.usr().getenv(envDisplay), x.auth.usr().getenv(envXauthority)); err != nil {
		return fmt.Errorf("could not open X display: %w", err)
	}
	return nil
}

// Gets Xorg Pid as int. Returns -1 when the carrier never started.
func (x *xorgSession) getCarrierPid() int {
	if x.xorg == nil || x.xorg.Process == nil {
		return -1
	}
	return x.xorg.Process.Pid
}

// Finishes Xorg as carrier for Xorg Session.
// Safe to call when the carrier failed to start.
func (x *xorgSession) finishCarrier() error {
	if x.xorg == nil {
		os.Remove(x.auth.usr().getenv(envXauthority))
		return nil
	}

	var err error
	if x.xorg.Process != nil {
		x.xorg.Process.Signal(os.Interrupt)
		err = x.xorg.Wait()
		logPrint("Interrupted Xorg")
	}

	os.Remove(x.auth.usr().getenv(envXauthority))
	logPrint("Cleaned up xauthority")

	if x.allowRootlessX() {
		if err := x.setTTYOwnership(x.conf, os.Getuid()); err != nil {
			logPrint(err)
		}
	}

	return err
}

// Sets TTY ownership to defined uid, but keeps the original gid.
func (x *xorgSession) setTTYOwnership(conf *config, uid int) error {
	info, err := os.Stat(conf.ttyPath())
	if err != nil {
		return err
	}
	stat := info.Sys().(*syscall.Stat_t)

	err = os.Chown(conf.ttyPath(), uid, int(stat.Gid))
	if err != nil {
		return err
	}
	err = os.Chmod(conf.ttyPath(), 0620)
	return err
}

// Finds free display for spawning Xorg instance.
func (x *xorgSession) getFreeXDisplay() string {
	for i := 0; i < 32; i++ {
		filename := fmt.Sprintf("/tmp/.X%d-lock", i)
		if !fileExists(filename) {
			return strconv.Itoa(i)
		}
	}
	return "0"
}

// Checks is rootless Xorg is allowed to be used
func (x *xorgSession) allowRootlessX() bool {
	return x.conf.RootlessXorg && (x.conf.DaemonMode || x.conf.ttyPath() == getCurrentTTYName("", true))
}
