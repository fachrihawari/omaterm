package procs

import (
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// soloFile is the part of Solo's solo.yml that ImportSolo reads.
type soloFile struct {
	Processes yaml.Node `yaml:"processes"`
}

// soloDef is one process in Solo's format.
type soloDef struct {
	Command            string            `yaml:"command"`
	WorkingDir         string            `yaml:"working_dir"`
	AutoStart          *bool             `yaml:"auto_start"` // Solo starts a process unless told not to
	AutoRestart        bool              `yaml:"auto_restart"`
	RestartWhenChanged []string          `yaml:"restart_when_changed"`
	Env                map[string]string `yaml:"env"`
}

// ImportSolo reads root/solo.yml, Solo's process file, as omaterm defs in the
// order it lists them. It returns nil when the file is absent or unreadable.
// The defs are not validated: the caller does that before it uses them.
func ImportSolo(root string) []Def {
	data, err := readFile(filepath.Join(root, "solo.yml"))
	if err != nil {
		return nil
	}
	var f soloFile
	if err := yaml.Unmarshal(data, &f); err != nil || f.Processes.Kind != yaml.MappingNode {
		return nil
	}
	var defs []Def
	for i := 0; i < len(f.Processes.Content); i += 2 {
		var s soloDef
		if err := f.Processes.Content[i+1].Decode(&s); err != nil {
			continue
		}
		d := Def{
			Name:    f.Processes.Content[i].Value,
			Command: s.Command,
			Dir:     s.WorkingDir,
			Env:     s.Env,
			Watch:   s.RestartWhenChanged,
		}
		// Solo starts a process unless auto_start is false.
		d.Autostart = s.AutoStart == nil || *s.AutoStart
		if s.AutoRestart {
			d.Restart = "always"
		}
		defs = append(defs, d)
	}
	return defs
}
