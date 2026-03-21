// Command adm is a TTY display manager (Wayland / X) using Bubble Tea for the UI.
package main

import (
	"os"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/0xADE/adm/internal/dm"
	"github.com/0xADE/adm/internal/ui"
)

func main() {
	runtime.LockOSThread()
	dm.TEST_MODE = false

	dm.ProcessCoreArgs(os.Args)
	conf := dm.LoadConfig(dm.LoadConfigPath(os.Args))
	dm.ProcessArgs(os.Args, conf)

	fTTY := dm.StartDaemon(conf)
	dm.InitLogger(conf)
	motd := dm.MotdText(conf)
	defer dm.StopDaemon(conf, fTTY)

	h := dm.InitSessionHandle()
	p := tea.NewProgram(ui.NewRoot(conf, motd, h), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		os.Exit(1)
	}
}
