package backup

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// pathKeys are the keys of each config file that say where this server keeps
// its data. A restore keeps their current values: the database is staged in
// the current data dir and the credentials go to the current auth dir, so a
// backup's older values would point the next start somewhere else.
// config.yaml names the auth dir as oauth.auth-dir, or as top-level auth-dir
// in the layout before upstream v8, which upstream still reads. Each form
// takes the current file's value or goes away, so the restored file names
// the auth dir the way the current file does.
var pathKeys = map[string][][]string{
	taprootEntry: {{"data-dir"}, {"store", "path"}},
	configEntry:  {{"oauth", "auth-dir"}, {"auth-dir"}},
}

// keepPathKeys returns restored with each key in keys set as the file at
// currentPath sets it, or removed where that file leaves it out. It returns
// restored unchanged when the values already agree.
func keepPathKeys(restored []byte, currentPath string, keys [][]string) ([]byte, error) {
	current, err := os.ReadFile(currentPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read current file: %w", err)
	}
	currentRoot, err := yamlRoot(current)
	if err != nil {
		// The current file does not parse, so it names no path to keep.
		currentRoot = nil
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(restored)) > 0 {
		if err := yaml.Unmarshal(restored, &doc); err != nil {
			return nil, fmt.Errorf("parse: %w", err)
		}
	}
	var root *yaml.Node
	if len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	changed := false
	for _, key := range keys {
		want := lookup(currentRoot, key)
		have := lookup(root, key)
		if sameScalar(want, have) {
			continue
		}
		if root == nil || root.Kind != yaml.MappingNode {
			if root != nil && (root.Kind != yaml.ScalarNode || root.Tag != "!!null") {
				return nil, errors.New("top level is not a mapping")
			}
			root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
		}
		if err := setOrDelete(root, key, want); err != nil {
			return nil, fmt.Errorf("keep %v: %w", key, err)
		}
		changed = true
	}
	if !changed {
		return restored, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return buf.Bytes(), nil
}

func yamlRoot(raw []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	return doc.Content[0], nil
}

// lookup returns the value at key, or nil when the path is absent.
func lookup(n *yaml.Node, key []string) *yaml.Node {
	for _, k := range key {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
			}
		}
		n = next
	}
	return n
}

// sameScalar treats an absent key and an empty or null value alike.
func sameScalar(a, b *yaml.Node) bool {
	value := func(n *yaml.Node) (string, bool) {
		if n == nil || n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
			return "", true
		}
		return n.Value, n.Kind == yaml.ScalarNode
	}
	va, okA := value(a)
	vb, okB := value(b)
	return okA && okB && va == vb
}

// setOrDelete sets key to a copy of value under mapping, creating parent
// mappings, or removes key when value is nil.
func setOrDelete(mapping *yaml.Node, key []string, value *yaml.Node) error {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key[0] {
			continue
		}
		if len(key) > 1 {
			child := mapping.Content[i+1]
			if child.Kind == yaml.ScalarNode && child.Tag == "!!null" && value != nil {
				child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				mapping.Content[i+1] = child
			}
			if child.Kind != yaml.MappingNode {
				if value == nil {
					return nil
				}
				return fmt.Errorf("%s is not a mapping", key[0])
			}
			return setOrDelete(child, key[1:], value)
		}
		if value == nil {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return nil
		}
		comments := *mapping.Content[i+1]
		replacement := *value
		replacement.HeadComment, replacement.LineComment, replacement.FootComment = comments.HeadComment, comments.LineComment, comments.FootComment
		mapping.Content[i+1] = &replacement
		return nil
	}
	if value == nil {
		return nil
	}
	if len(key) > 1 {
		child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key[0]}, child)
		return setOrDelete(child, key[1:], value)
	}
	replacement := *value
	replacement.HeadComment, replacement.LineComment, replacement.FootComment = "", "", ""
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key[0]}, &replacement)
	return nil
}
