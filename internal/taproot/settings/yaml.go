package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// yamlEdit sets one key in taproot.yaml, creating parent mappings as needed.
type yamlEdit struct {
	path  []string
	value any
}

// writeYAML applies edits to the file's node tree, so unrelated keys and most
// comments survive, then replaces the file atomically.
func writeYAML(path string, edits []yamlEdit) error {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	mode := fs.FileMode(0o600)
	if info, errStat := os.Stat(path); errStat == nil {
		mode = info.Mode().Perm()
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("parse: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		// A file holding only comments parses to a null scalar; keep its comments.
		if root.Kind != yaml.ScalarNode || root.Tag != "!!null" {
			return errors.New("top level is not a mapping")
		}
		root.Kind, root.Tag, root.Value = yaml.MappingNode, "!!map", ""
	}
	for _, e := range edits {
		if err := setPath(root, e.path, e.value); err != nil {
			return fmt.Errorf("set %v: %w", e.path, err)
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return replaceFile(path, buf.Bytes(), mode)
}

func setPath(mapping *yaml.Node, path []string, value any) error {
	key := path[0]
	var existing *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			existing = mapping.Content[i+1]
			break
		}
	}
	if len(path) > 1 {
		if existing == nil {
			existing = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, existing)
		} else if existing.Kind != yaml.MappingNode {
			// "access:" with nothing under it is null; anything else is a type error.
			if existing.Kind != yaml.ScalarNode || existing.Tag != "!!null" {
				return fmt.Errorf("%s is not a mapping", key)
			}
			existing.Kind, existing.Tag, existing.Value = yaml.MappingNode, "!!map", ""
		}
		return setPath(existing, path[1:], value)
	}
	var next yaml.Node
	if err := next.Encode(value); err != nil {
		return err
	}
	if existing == nil {
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &next)
		return nil
	}
	next.HeadComment, next.LineComment, next.FootComment = existing.HeadComment, existing.LineComment, existing.FootComment
	// Keep a flow list flow. Scalars take the encoder's style so a value that
	// needs quotes gets them.
	if existing.Kind == yaml.SequenceNode && next.Kind == yaml.SequenceNode {
		next.Style = existing.Style
	}
	*existing = next
	return nil
}

// replaceFile writes beside the target and renames over it, so a crash leaves
// either the old file or the new one.
func replaceFile(path string, data []byte, mode fs.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
