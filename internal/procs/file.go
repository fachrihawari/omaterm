package procs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FileName is the file a project keeps its shared processes in, at its root.
const FileName = "omaterm.yml"

// maxFile is the largest omaterm.yml that is read or written.
const maxFile = 1 << 20

// LoadFile reads the processes of the project at root, in the order the file
// lists them. A missing file has none.
func LoadFile(root string) ([]Def, error) {
	data, err := readFile(filepath.Join(root, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	doc, err := parse(data)
	if err != nil {
		return nil, err
	}
	top, err := rootMapping(doc)
	if err != nil {
		return nil, err
	}
	procs := mappingValue(top, "processes")
	if procs == nil || procs.Tag == "!!null" {
		return nil, nil
	}
	if procs.Kind != yaml.MappingNode {
		return nil, errors.New("the processes must be a mapping")
	}
	var defs []Def
	seen := map[string]bool{}
	for i := 0; i < len(procs.Content); i += 2 {
		name := procs.Content[i].Value
		if seen[name] {
			return nil, fmt.Errorf("the process %q is listed twice", name)
		}
		seen[name] = true
		var d Def
		if err := procs.Content[i+1].Decode(&d); err != nil {
			return nil, fmt.Errorf("the process %q: %w", name, err)
		}
		d.Name = name
		defs = append(defs, d)
	}
	return defs, nil
}

// SaveFile writes defs as the processes of the project at root. It edits the
// file it finds rather than rewriting it: comments, the order of entries it
// keeps, and any other top-level key stay as they were. Local defs are not
// part of the file and are skipped.
func SaveFile(root string, defs []Def) error {
	var shared []Def
	seen := map[string]bool{}
	for _, d := range defs {
		if d.Local {
			continue
		}
		if err := d.Validate(root); err != nil {
			return fmt.Errorf("the process %q: %w", d.Name, err)
		}
		if seen[d.Name] {
			return fmt.Errorf("the process %q is listed twice", d.Name)
		}
		seen[d.Name] = true
		shared = append(shared, d)
	}

	path := filepath.Join(root, FileName)
	mode := fs.FileMode(0o644)
	data, err := readFile(path)
	switch {
	case err == nil:
		if info, serr := os.Stat(path); serr == nil {
			mode = info.Mode().Perm()
		}
	case errors.Is(err, fs.ErrNotExist):
		data = nil
	default:
		return err
	}
	doc, err := parse(data)
	if err != nil {
		return err
	}
	top, err := rootMapping(doc)
	if err != nil {
		return err
	}
	procs := mappingValue(top, "processes")
	if procs == nil {
		procs = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "processes"}, procs)
	}
	if procs.Tag == "!!null" {
		// An empty `processes:` is still the key to fill, so it becomes a mapping in place.
		procs.Kind, procs.Tag, procs.Value = yaml.MappingNode, "!!map", ""
	}
	if procs.Kind != yaml.MappingNode {
		return errors.New("the processes must be a mapping")
	}

	// Each entry it already has, by name, with its key and value nodes.
	old := map[string][2]*yaml.Node{}
	for i := 0; i < len(procs.Content); i += 2 {
		old[procs.Content[i].Value] = [2]*yaml.Node{procs.Content[i], procs.Content[i+1]}
	}
	var content []*yaml.Node
	for _, d := range shared {
		prev, ok := old[d.Name]
		if ok {
			// An unchanged entry keeps its text, so only a changed one is re-encoded.
			var before Def
			if prev[1].Decode(&before) == nil && sameDef(before, d) {
				content = append(content, prev[0], prev[1])
				continue
			}
		}
		val := &yaml.Node{}
		if err := val.Encode(d); err != nil {
			return err
		}
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: d.Name}
		if ok {
			// A changed entry keeps its key, and so the comments above it.
			key = prev[0]
		}
		content = append(content, key, val)
	}
	procs.Content = content

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeAtomic(path, buf.Bytes(), mode)
}

// sameDef reports whether saving d over the entry before changes nothing the
// file records. Name and Local are not compared: the entry is found by name,
// and Local defs are not saved.
func sameDef(before, d Def) bool {
	return before.Variant() == d.Variant() && before.Autostart == d.Autostart
}

// readFile reads a file that may be at most maxFile bytes long.
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("%s is larger than 1 MB", FileName)
	}
	return data, nil
}

// parse reads data as a YAML document. Empty data, or data with comments
// only, still gives a document, so that a save can fill it in.
func parse(data []byte) (*yaml.Node, error) {
	doc := &yaml.Node{}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	return doc, nil
}

// rootMapping is the top-level mapping of doc. A file whose top level is
// empty (null) is taken as an empty mapping.
func rootMapping(doc *yaml.Node) (*yaml.Node, error) {
	top := doc.Content[0]
	if top.Tag == "!!null" {
		top.Kind, top.Tag, top.Value = yaml.MappingNode, "!!map", ""
	}
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping at the top", FileName)
	}
	return top, nil
}

// mappingValue is the value of key in the mapping m, or nil when m has no such key.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// writeAtomic replaces path with data through a temporary file in the same
// folder, so a crash leaves the old file or the new one, never half of one.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
