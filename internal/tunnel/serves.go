package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Tailscale serves are shared by every program on the machine: what the user
// serves themselves must be left alone, and what omaterm serves must not
// outlive a daemon that crashed before it could turn it off.

// PortInUseError is Tailscale already serving another address on a port
// omaterm was to use: serving ours would replace it.
type PortInUseError struct{ Port int }

func (e *PortInUseError) Error() string {
	return fmt.Sprintf("Tailscale already serves another address on %d: free it", e.Port)
}

// TailscaleServe is what Tailscale serves over https and tcp, by port: the
// local addresses each port proxies to (none for a port that serves a file
// or text). A port that is a key serves something.
func TailscaleServe() (map[int][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tailscaleBin, "serve", "status", "--json")
	hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tailscale serve status: %w", err)
	}
	return parseServe(out)
}

// serveConfig is the part of tailscale serve status --json read here. A
// serve in the foreground is the same, one level down.
type serveConfig struct {
	TCP map[string]json.RawMessage
	Web map[string]struct {
		Handlers map[string]struct{ Proxy string }
	}
	Foreground map[string]*serveConfig
}

func parseServe(data []byte) (map[int][]string, error) {
	var cfg serveConfig
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[int][]string{}, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("tailscale serve status: %w", err)
	}
	ports := map[int][]string{}
	var walk func(*serveConfig)
	walk = func(c *serveConfig) {
		for p := range c.TCP {
			if n, err := strconv.Atoi(p); err == nil {
				if _, ok := ports[n]; !ok {
					ports[n] = nil
				}
			}
		}
		for hostport, web := range c.Web {
			_, p, err := net.SplitHostPort(hostport)
			n, aerr := strconv.Atoi(p)
			if err != nil || aerr != nil {
				continue
			}
			for _, h := range web.Handlers {
				if h.Proxy != "" {
					ports[n] = append(ports[n], h.Proxy)
				}
			}
			if _, ok := ports[n]; !ok {
				ports[n] = nil
			}
		}
		for _, fg := range c.Foreground {
			if fg != nil {
				walk(fg)
			}
		}
	}
	walk(&cfg)
	return ports, nil
}

// checkServePort refuses a port that serves an address other than target.
// A serve that can't be read means no refusal: it is logged, not trusted.
func checkServePort(port int, target string) error {
	serves, err := TailscaleServe()
	if err != nil {
		log.Printf("tunnel: %v: assuming nothing is served on %d", err, port)
		return nil
	}
	targets, used := serves[port]
	if !used {
		return nil
	}
	for _, t := range targets {
		if t != target {
			return &PortInUseError{Port: port}
		}
	}
	if len(targets) == 0 {
		return &PortInUseError{Port: port}
	}
	return nil
}

// serveLog is the file of the https ports omaterm turned on, which a daemon
// that crashed leaves behind it; serveMu orders it.
var (
	serveMu   sync.Mutex
	servePath string
)

// TrackServes keeps, in the file at path, the Tailscale https ports omaterm
// serves on: "" stops keeping them.
func TrackServes(path string) {
	serveMu.Lock()
	servePath = path
	serveMu.Unlock()
}

func readServeLog(path string) []int {
	var list struct{ Ports []int }
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &list)
	}
	return list.Ports
}

func writeServeLog(path string, ports []int) error {
	sort.Ints(ports)
	b, _ := json.Marshal(struct{ Ports []int }{ports})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// trackServe notes a port omaterm serves on, or no longer does.
func trackServe(port int, on bool) {
	serveMu.Lock()
	defer serveMu.Unlock()
	if servePath == "" {
		return
	}
	var ports []int
	for _, p := range readServeLog(servePath) {
		if p != port {
			ports = append(ports, p)
		}
	}
	if on {
		ports = append(ports, port)
	}
	if err := writeServeLog(servePath, ports); err != nil {
		log.Printf("tunnel: keeping the served ports: %v", err)
	}
}

// omatermTarget is an address omaterm serves: its own web page or a
// process's port, on the loopback.
var omatermTarget = regexp.MustCompile(`^http://127\.0\.0\.1:\d+$`)

// CleanupServes turns off the ports a daemon before this one left serving,
// as the file keeps them, when they still serve an address of omaterm's: one
// the user has put something else on is theirs. The file is emptied. It
// returns the ports it turned off.
func CleanupServes() ([]int, error) {
	serveMu.Lock()
	path := servePath
	serveMu.Unlock()
	if path == "" {
		return nil, nil
	}
	listed := readServeLog(path)
	if len(listed) == 0 {
		return nil, nil
	}
	serves, err := TailscaleServe()
	if err != nil {
		// Tailscale isn't there (or signed out): nothing of ours serves.
		return nil, errors.Join(err, writeServeLog(path, nil))
	}
	var off []int
	var errs []error
	for _, port := range listed {
		targets := serves[port]
		if len(targets) == 0 {
			continue
		}
		ours := true
		for _, t := range targets {
			ours = ours && omatermTarget.MatchString(t)
		}
		if !ours {
			continue
		}
		if err := serveOff(port); err != nil {
			errs = append(errs, err)
			continue
		}
		off = append(off, port)
	}
	errs = append(errs, writeServeLog(path, nil))
	return off, errors.Join(errs...)
}
