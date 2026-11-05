package daemon

import "strings"

// keptLines is how many of its last lines a process's run keeps once it
// ends, and keptBytes caps them: read answers with them for its pane, so
// that web access shows why a process stopped.
const (
	keptLines = 500
	keptBytes = 256 << 10
)

// keptOutput is the last output of a process session's run that ended:
// the run's pane, and its text.
type keptOutput struct {
	pane string
	text string
}

// keepOutput keeps a process's pane's last lines as the pane goes, in
// place of its session's run before. They stay in memory only: the
// pane's scrollback on disk goes with the pane, as it did.
func (d *Daemon) keepOutput(id string) {
	p := d.reg.Get(id)
	if p == nil {
		return
	}
	text := p.Text(true, keptLines)
	if len(text) > keptBytes {
		text = text[len(text)-keptBytes:]
		if i := strings.IndexByte(text, '\n'); i >= 0 && i < len(text)-1 {
			text = text[i+1:]
		}
	}
	d.procMu.Lock()
	defer d.procMu.Unlock()
	if d.procOutput == nil {
		d.procOutput = map[string]keptOutput{}
	}
	d.procOutput[p.Info().Session] = keptOutput{pane: id, text: text}
}

// keptOutput is what a gone process pane kept, its last lines lines when
// lines > 0.
func (d *Daemon) keptOutput(id string, lines int) (string, bool) {
	d.procMu.Lock()
	defer d.procMu.Unlock()
	for _, k := range d.procOutput {
		if k.pane != id {
			continue
		}
		all := strings.Split(strings.TrimSuffix(k.text, "\n"), "\n")
		if lines <= 0 || len(all) <= lines {
			return k.text, true
		}
		return strings.Join(all[len(all)-lines:], "\n") + "\n", true
	}
	return "", false
}
