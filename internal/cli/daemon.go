package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
)

// daemonCmd looks after the session daemon, which runs the sessions'
// shells so they outlive the window.
func daemonCmd(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	socket := paths.DaemonSocketPath()
	switch sub {
	case "status":
		c, err := client.Dial(socket)
		if err != nil {
			fmt.Println("not running")
		} else {
			defer c.Close()
			var v struct {
				App string
				PID int
			}
			c.Call("version", nil, &v)
			var panes map[string]any
			c.Call("panes", nil, &panes)
			fmt.Printf("running omaterm %s, pid %d, %d panes\n", v.App, v.PID, len(panes))
		}
		if daemon.ServiceInstalled() {
			fmt.Printf("service: %s\n", shortDir(daemon.UnitPath()))
		}
		return nil
	case "stop":
		replaced, err := stopDaemon(socket)
		if replaced {
			fmt.Println("stopped; the open app started a new daemon, its sessions restored")
		}
		return err
	case "restart":
		replaced, err := stopDaemon(socket)
		if err != nil || replaced {
			return err
		}
		return daemon.StartProcess()
	case "serve":
		return daemon.Run(daemon.Options{Serve: true, Restore: true, Version: Build})
	case "install-service":
		return installService()
	case "uninstall-service":
		if !daemon.ServiceInstalled() {
			return nil
		}
		exec.Command("systemctl", "--user", "disable", "--now", daemon.ServiceName()).Run()
		if err := os.Remove(daemon.UnitPath()); err != nil {
			return err
		}
		exec.Command("systemctl", "--user", "daemon-reload").Run()
		fmt.Println("removed", shortDir(daemon.UnitPath()))
		return nil
	}
	return usageErr("omaterm daemon status|stop|restart|serve|install-service|uninstall-service")
}

// stopDaemon stops the daemon, its sessions with it, once it saved their
// scrollback; the app starts them again from it. It reports whether a
// new daemon answers already: an open app starts one at once.
func stopDaemon(socket string) (replaced bool, err error) {
	if daemon.ServiceInstalled() {
		exec.Command("systemctl", "--user", "stop", daemon.ServiceName()).Run()
	}
	pid := func() int {
		c, err := client.Dial(socket)
		if err != nil {
			return 0
		}
		defer c.Close()
		return c.Welcome.PID
	}
	c, err := client.Dial(socket)
	if err != nil {
		return false, nil
	}
	old := c.Welcome.PID
	c.Call("shutdown", nil, nil)
	c.Close()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if p := pid(); p != old {
			return p != 0, nil
		}
	}
	return false, errors.New("the daemon did not stop")
}

// installService installs and starts the systemd user unit, which runs
// the daemon from the omaterm command's stable link, so an update keeps
// it.
func installService() error {
	if runtime.GOOS == "windows" {
		return daemon.ErrNoDaemon
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl not found: the service needs systemd")
	}
	exe := filepath.Join(paths.BinDir(), "omaterm")
	if _, err := os.Stat(exe); err != nil {
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	path := daemon.UnitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(daemon.Unit(exe)), 0o644); err != nil {
		return err
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	// A daemon the app started gives way to the service's.
	stopDaemon(paths.DaemonSocketPath())
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", daemon.ServiceName()).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable: %s", out)
	}
	fmt.Printf("installed %s\n", shortDir(path))
	fmt.Println("sessions now survive logging out once lingering is on: loginctl enable-linger")
	return nil
}

// clientsCmd tells who the session daemon serves: the app, if open, and
// the clients showing each pane.
func clientsCmd(args []string) error {
	f, err := parse(args, nil, map[string]string{"json": "json"})
	if err != nil {
		return err
	}
	c, err := client.Dial(paths.DaemonSocketPath())
	if err != nil {
		return fmt.Errorf("%w: the session daemon is not running", errUnreachable)
	}
	defer c.Close()
	var cl proto.Clients
	if err := c.Call("clients", nil, &cl); err != nil {
		return err
	}
	if f.set["json"] {
		return printJSON(cl)
	}
	app := "closed"
	if cl.Apps > 0 {
		app = "open"
	}
	fmt.Printf("app: %s\n", app)
	ids := make([]string, 0, len(cl.Attached))
	for id := range cl.Attached {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Printf("pane %s: %d shown\n", id, cl.Attached[id])
	}
	return nil
}
