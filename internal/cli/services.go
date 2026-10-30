package cli

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/proto"
)

// servicesCmd lists the ports the viewed project's processes listen on,
// or waits until one accepts connections. A dev server an agent starts is
// the usual case: it is up before the agent runs its tests, and the user
// gets a URL to open.
func servicesCmd(c *client.Client, args []string) error {
	f, err := parse(args, map[string]string{"t": "timeout", "timeout": "timeout", "wait": "wait"},
		map[string]string{"json": "json"})
	if err != nil {
		return err
	}
	if len(f.pos) > 0 {
		return usageErr("omaterm services [--wait <name|port>] [-t timeout] [--json]")
	}
	st, err := state(c)
	if err != nil {
		return err
	}
	want := f.vals["wait"]
	if want == "" {
		if f.set["json"] {
			return printJSON(st.Ports)
		}
		listPorts(st)
		return nil
	}
	ms, err := parseTimeout(f.vals["timeout"])
	if err != nil {
		return err
	}
	if ms == 0 {
		ms = int64(serviceWait * time.Millisecond)
	}
	return waitForPort(c, want, time.Duration(ms)*time.Millisecond, f.set["json"])
}

// serviceWait is how long omaterm services --wait waits by default: long
// enough for a dev server to install, build and bind, short enough that a
// wrong name fails before a person gives up on it.
const serviceWait = 2 * time.Minute

// listPorts prints the ports a project's processes listen on, and says so
// when there are none to print: the app scans for them, so a state the
// daemon answered has none.
func listPorts(st proto.State) {
	if st.Workspace == "" {
		fmt.Println("no ports: the app scans for them, and its window is closed")
		return
	}
	if len(st.Ports) == 0 {
		fmt.Printf("no listening ports in %s\n", shortDir(st.Workspace))
		return
	}
	fmt.Printf("%-6s %-14s %s\n", "PORT", "PROCESS", "URL")
	for _, p := range st.Ports {
		fmt.Printf("%-6d %-14s %s\n", p.Port, fit(p.Name, 14), p.URL)
	}
}

// fit shortens a name to the column it is printed in, so the URL after it
// stays in line.
func fit(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// waitForPort waits until the port named, or numbered, accepts a
// connection, printing it. It asks the app again on every turn of the
// wait: a dev server that has not bound yet has no port to list.
func waitForPort(c *client.Client, want string, timeout time.Duration, asJSON bool) error {
	deadline := time.Now().Add(timeout)
	// A number is a port the caller knows, even before anything listens on
	// it; a name is a process, which the app only sees once it runs.
	num, numbered := portNumber(want)
	for {
		st, err := state(c)
		if err != nil {
			return err
		}
		if p, ok := matchPort(st.Ports, want, num, numbered); ok && dials(p.Port) {
			if asJSON {
				return printJSON(p)
			}
			fmt.Printf("%d %s %s\n", p.Port, p.Name, p.URL)
			return nil
		}
		if time.Now().After(deadline) {
			if numbered {
				return fmt.Errorf("port %d did not accept a connection within %s", num, timeout)
			}
			return fmt.Errorf("no port of %q accepted a connection within %s", want, timeout)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

// portNumber reads a target that is a port number, as opposed to a name.
func portNumber(want string) (int, bool) {
	n, err := strconv.Atoi(want)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

// matchPort finds the port waited for: the one numbered, or the one whose
// process or service name holds the name given.
func matchPort(ports []proto.Port, want string, num int, numbered bool) (proto.Port, bool) {
	if numbered {
		for _, p := range ports {
			if p.Port == num {
				return p, true
			}
		}
		return proto.Port{Port: num, URL: fmt.Sprintf("http://localhost:%d", num), Open: true}, true
	}
	lower := strings.ToLower(want)
	for _, p := range ports {
		if strings.Contains(strings.ToLower(p.Name), lower) || strings.Contains(strings.ToLower(p.Process), lower) {
			return p, true
		}
	}
	return proto.Port{}, false
}

// dials reports whether something accepts a connection on the port now.
func dials(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
