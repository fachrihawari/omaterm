package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"omaterm/internal/procs"
	"omaterm/internal/tunnel"
)

// A share makes a process reachable from elsewhere, through a tunnel to its
// port: Tailscale for the user's own devices, a Cloudflare quick tunnel for
// anyone with the link. It follows the process: when the process stops or
// comes back on another port the tunnel ends, and starts again on the new
// port, as long as the share was asked for and not ended by the user.

// shareFirstPort and shareNextPort are the https ports Tailscale shares take:
// 8443, then 10000 and up. Never 443, which web access may hold.
const (
	shareFirstPort = 8443
	shareNextPort  = 10000
)

// share is one process's, asked for by the user.
type share struct {
	via tunnel.Provider
	// tun is the running tunnel, nil while there is none (the process isn't
	// up, or the tunnel is starting); port is the process's port it reaches.
	tun      tunnel.Tunnel
	port     int
	tsPort   int // the https port a Tailscale share holds
	starting bool
}

// shareTimeout bounds starting a tunnel for a share.
var shareTimeout = 45 * time.Second

// shareProcess asks for a share of a process. A process that is up is shared
// at once, and the call tells why it could not; one that isn't is shared
// when it is.
func (d *Daemon) shareProcess(project, name string, via tunnel.Provider) error {
	if via != tunnel.Tailscale && via != tunnel.Cloudflare {
		return fmt.Errorf("share via tailscale or cloudflare, not %q", via)
	}
	proc, ok := d.findProc(project, name)
	if !ok {
		return fmt.Errorf("%s: %w", name, procs.ErrNotFound)
	}
	if proc.Def.Port == 0 && proc.URL == "" {
		return fmt.Errorf("%s has no port to share: give it a port or a url", name)
	}
	key := procKey{project, name}
	sh := &share{via: via}
	d.shareMu.Lock()
	old := d.shares[key]
	d.shares[key] = sh
	delete(d.shareNotes, key)
	d.shareMu.Unlock()
	if old != nil {
		d.endShare(old)
	}
	var err error
	if proc.Status == procs.Running && proc.Port > 0 {
		err = d.startShare(key, sh, proc.Port)
	}
	d.changed()
	return err
}

// unshareProcess ends a process's share, and forgets why one failed.
func (d *Daemon) unshareProcess(project, name string) error {
	key := procKey{project, name}
	d.shareMu.Lock()
	sh := d.shares[key]
	delete(d.shares, key)
	delete(d.shareNotes, key)
	d.shareMu.Unlock()
	if sh != nil {
		d.endShare(sh)
	}
	d.changed()
	return nil
}

func (d *Daemon) findProc(project, name string) (procs.Proc, bool) {
	for _, p := range d.procs.List(project) {
		if p.Name == name {
			return p, true
		}
	}
	return procs.Proc{}, false
}

// startShare makes sh's tunnel to port, and keeps it: a tunnel that ends by
// itself ends the share, with a note saying why. It returns why it could
// not make one; the share is gone then, and its process says so.
func (d *Daemon) startShare(key procKey, sh *share, port int) error {
	d.shareMu.Lock()
	if d.shares[key] != sh || sh.starting || sh.tun != nil {
		d.shareMu.Unlock()
		return nil
	}
	sh.starting = true
	spec := tunnel.Spec{Provider: sh.via, Target: fmt.Sprintf("http://127.0.0.1:%d", port), Name: key.name}
	if sh.via == tunnel.Tailscale {
		sh.tsPort = d.freeSharePortLocked()
		spec.Port = sh.tsPort
	}
	d.shareMu.Unlock()

	tunnelMu.Lock()
	start := startTunnel
	tunnelMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), shareTimeout)
	tun, err := start(ctx, spec)
	cancel()

	d.shareMu.Lock()
	sh.starting = false
	if d.shares[key] != sh {
		// Unshared while it started.
		d.shareMu.Unlock()
		if tun != nil {
			tun.Stop()
		}
		return nil
	}
	if err != nil {
		delete(d.shares, key)
		d.shareNotes[key] = "sharing failed: " + err.Error()
		d.shareMu.Unlock()
		log.Printf("share %s: %v", key.name, err)
		d.changed()
		return err
	}
	sh.tun, sh.port = tun, port
	d.shareMu.Unlock()
	d.changed()
	go d.watchShare(key, sh, tun)
	return nil
}

// watchShare waits for a tunnel to end. One that failed ends the share; one
// stopped by us is already out of it.
func (d *Daemon) watchShare(key procKey, sh *share, tun tunnel.Tunnel) {
	<-tun.Done()
	err := tun.Err()
	d.shareMu.Lock()
	if d.shares[key] != sh || sh.tun != tun {
		d.shareMu.Unlock()
		return
	}
	if err == nil {
		sh.tun, sh.tsPort = nil, 0
		d.shareMu.Unlock()
		return
	}
	delete(d.shares, key)
	d.shareNotes[key] = "sharing ended: " + err.Error()
	d.shareMu.Unlock()
	d.changed()
}

// freeSharePortLocked is the lowest https port no share of ours holds.
func (d *Daemon) freeSharePortLocked() int {
	held := map[int]bool{}
	for _, sh := range d.shares {
		if sh.tsPort != 0 {
			held[sh.tsPort] = true
		}
	}
	if !held[shareFirstPort] {
		return shareFirstPort
	}
	port := shareNextPort
	for held[port] {
		port++
	}
	return port
}

// endShare stops the tunnel of a share that is no longer wanted.
func (d *Daemon) endShare(sh *share) {
	d.shareMu.Lock()
	tun := sh.tun
	sh.tun, sh.tsPort = nil, 0
	d.shareMu.Unlock()
	if tun != nil {
		if err := tun.Stop(); err != nil {
			log.Printf("share: %v", err)
		}
	}
}

// reconcileShares brings the tunnels in line with the processes: one ends
// when its process isn't up or has another port, and a share asked for
// starts again when its process is up. The supervisor's changes call it.
func (d *Daemon) reconcileShares() {
	d.shareMu.Lock()
	n := len(d.shares)
	d.shareMu.Unlock()
	if n == 0 {
		return
	}
	state := map[procKey]procs.Proc{}
	for _, p := range d.procs.All() { // before shareMu: the supervisor's lock comes first
		state[procKey{p.Project, p.Name}] = p.Proc
	}
	type action struct {
		key  procKey
		sh   *share
		stop bool
		port int
	}
	var todo []action
	d.shareMu.Lock()
	for key, sh := range d.shares {
		p, ok := state[key]
		up := ok && p.Status == procs.Running && p.Port > 0
		switch {
		case !ok:
			delete(d.shares, key)
			todo = append(todo, action{key: key, sh: sh, stop: true})
		case sh.tun != nil && (!up || p.Port != sh.port):
			// On another port it starts again at once.
			a := action{key: key, sh: sh, stop: true}
			if up {
				a.port = p.Port
			}
			todo = append(todo, a)
		case sh.tun == nil && !sh.starting && up:
			todo = append(todo, action{key: key, sh: sh, port: p.Port})
		}
	}
	d.shareMu.Unlock()
	if len(todo) == 0 {
		return
	}
	go func() {
		for _, a := range todo {
			if a.stop {
				d.endShare(a.sh)
				if a.port == 0 {
					continue
				}
			}
			if err := d.startShare(a.key, a.sh, a.port); err != nil && !errors.Is(err, tunnel.ErrUnavailable) {
				log.Printf("share %s: %v", a.key.name, err)
			}
		}
		d.changed()
	}()
}

// stopShares ends every share, as the daemon stops.
func (d *Daemon) stopShares() {
	d.shareMu.Lock()
	all := d.shares
	d.shares = map[procKey]*share{}
	d.shareMu.Unlock()
	for _, sh := range all {
		d.endShare(sh)
	}
}

// decorateShare gives a process the address it is shared at, or the note
// that sharing failed. It reads only the shares, so the supervisor can call
// it with its lock held.
func (d *Daemon) decorateShare(project string, p *procs.Proc) {
	key := procKey{project, p.Name}
	d.shareMu.Lock()
	defer d.shareMu.Unlock()
	if sh := d.shares[key]; sh != nil && sh.tun != nil {
		p.Share, p.ShareVia = sh.tun.URL(), string(sh.via)
	}
	if note := d.shareNotes[key]; note != "" && p.Message == "" {
		p.Message = note
	}
}
