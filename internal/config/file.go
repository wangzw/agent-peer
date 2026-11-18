package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type configDocument struct {
	document *yaml.Node
	root     *yaml.Node
	values   map[string]Value
	present  map[string]bool
	invalid  map[string]error
	exists   bool
	// empty reports that the bytes held no YAML document at all: an empty
	// file, one containing only whitespace and comments, or a document with no
	// content. Load treats such a file as an empty configuration, but a reload
	// must treat it as unparseable so a truncation window cannot reset every
	// value to its default.
	empty       bool
	headComment string
	// contentHash is the hash of exactly the bytes that were parsed into this
	// document. Load copies it into core.lastFileHash, so the first reload
	// comparison starts from the hash of the bytes these values came from.
	// Reloads themselves hash the file on disk and compare core.lastFileHash;
	// a document built by parseConfigFile during a reload leaves this field
	// zero.
	contentHash [32]byte
}

func emptyDocument() configDocument {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return configDocument{
		document: doc,
		root:     root,
		values:   make(map[string]Value),
		present:  make(map[string]bool),
		invalid:  make(map[string]error),
	}
}

func readConfigFile(path string) (configDocument, error) {
	return readConfigFileFrom(path, os.ReadFile)
}

// readConfigFileFrom reads the file through readFile exactly once, and records
// the hash of the same bytes it parsed. A second read to hash the file could
// see different content, leaving a stored hash that describes bytes the parsed
// values never came from.
func readConfigFileFrom(path string, readFile func(string) ([]byte, error)) (configDocument, error) {
	data, err := readFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			doc := emptyDocument()
			doc.empty = true
			return doc, nil
		}
		return configDocument{}, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	hash := sha256.Sum256(data)
	doc, err := parseConfigFile(data)
	if err != nil {
		return configDocument{}, err
	}
	doc.exists = true
	doc.contentHash = hash
	return doc, nil
}

func parseConfigFile(data []byte) (configDocument, error) {
	doc := emptyDocument()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var parsed yaml.Node
	err := dec.Decode(&parsed)
	if errors.Is(err, io.EOF) {
		doc.empty = true
		doc.headComment = leadingComment(data)
		return doc, nil
	}
	if err != nil {
		return configDocument{}, fmt.Errorf("config file parse error: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil && extra.Kind != 0 {
		return configDocument{}, errors.New("config file parse error: multiple YAML documents")
	} else if err != nil && !errors.Is(err, io.EOF) {
		return configDocument{}, fmt.Errorf("config file parse error: %w", err)
	}
	root := &parsed
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			doc.empty = true
			doc.headComment = leadingComment(data)
			return doc, nil
		}
		root = root.Content[0]
	}
	root = resolveAliasNode(root)
	if root == nil || root.Kind == 0 {
		doc.empty = true
		doc.headComment = leadingComment(data)
		return doc, nil
	}
	if root.Kind != yaml.MappingNode {
		return configDocument{}, fmt.Errorf("config file parse error: top level must be a mapping at line %d, column %d", root.Line, root.Column)
	}
	if err := checkStructure(root); err != nil {
		return configDocument{}, err
	}
	for _, section := range knownSections() {
		n, ok := findSection(root, section)
		if !ok {
			continue
		}
		if n == nil || n.Kind != yaml.MappingNode {
			line, column := sectionPosition(root, section)
			return configDocument{}, fmt.Errorf("config file parse error: section %q must be a mapping at line %d, column %d", section, line, column)
		}
	}
	doc.document = &parsed
	doc.root = root
	for _, name := range allKeyNames() {
		keyNode, ok := findKeyNode(root, name)
		if !ok {
			continue
		}
		doc.present[name] = true
		spec, _ := lookupKey(name)
		v, err := parseNode(spec, keyNode, false)
		if err != nil {
			doc.invalid[name] = err
			continue
		}
		doc.values[name] = v
	}
	return doc, nil
}

// leadingComment returns the whole file when it contains only blank lines and
// comments. Such a document has no YAML nodes to carry the comments.
func leadingComment(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return ""
		}
	}
	return string(data)
}

func checkStructure(n *yaml.Node) error {
	n = resolveAliasNode(n)
	if n == nil {
		return errors.New("config file parse error: unresolved alias")
	}
	switch n.Kind {
	case yaml.MappingNode:
		seen := make(map[string]bool, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := resolveAliasNode(n.Content[i])
			if k == nil || k.Kind != yaml.ScalarNode {
				return fmt.Errorf("config file parse error: mapping key at line %d, column %d is not a scalar", n.Line, n.Column)
			}
			if k.Value == "<<" {
				return fmt.Errorf("config file parse error: merge keys are not supported at line %d, column %d", k.Line, k.Column)
			}
			if seen[k.Value] {
				return fmt.Errorf("config file parse error: duplicate key %q at line %d, column %d", k.Value, k.Line, k.Column)
			}
			seen[k.Value] = true
			if err := checkStructure(n.Content[i+1]); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if err := checkStructure(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func sectionAndLeaf(key string) (section, leaf string) {
	if i := strings.IndexByte(key, '.'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

func knownSections() []string {
	seen := make(map[string]bool)
	var out []string
	for name := range keyTable {
		section, _ := sectionAndLeaf(name)
		if !seen[section] {
			seen[section] = true
			out = append(out, section)
		}
	}
	sort.Strings(out)
	return out
}

func sectionPosition(root *yaml.Node, section string) (line, column int) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := resolveAliasNode(root.Content[i])
		if k != nil && k.Kind == yaml.ScalarNode && k.Value == section {
			return k.Line, k.Column
		}
	}
	return root.Line, root.Column
}

func findSection(root *yaml.Node, section string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := resolveAliasNode(root.Content[i])
		if k != nil && k.Kind == yaml.ScalarNode && k.Value == section {
			return resolveAliasNode(root.Content[i+1]), true
		}
	}
	return nil, false
}

func findKeyNode(root *yaml.Node, key string) (*yaml.Node, bool) {
	section, leaf := sectionAndLeaf(key)
	m, ok := findSection(root, section)
	if !ok {
		return nil, false
	}
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := resolveAliasNode(m.Content[i])
		if k != nil && k.Kind == yaml.ScalarNode && k.Value == leaf {
			return resolveAliasNode(m.Content[i+1]), true
		}
	}
	return nil, false
}

func (d *configDocument) nodeFor(key string) *yaml.Node {
	if d == nil || d.root == nil {
		return nil
	}
	n, _ := findKeyNode(d.root, key)
	return n
}

func (d *configDocument) setKey(key string, v Value) error {
	spec, ok := lookupKey(key)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	if d.root == nil {
		d.document = nil
		*d = emptyDocument()
	}
	if d.root.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: %s: top level is not a mapping", ErrInvalidValue, key)
	}
	section, leaf := sectionAndLeaf(key)
	m, ok := findSection(d.root, section)
	if !ok {
		sectionNode := &yaml.Node{
			Kind:  yaml.MappingNode,
			Tag:   "!!map",
			Value: "",
		}
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: section}
		d.root.Content = append(d.root.Content, keyNode, sectionNode)
		m = sectionNode
	}
	if m == nil || m.Kind != yaml.MappingNode {
		return fmt.Errorf("%w: %s: section %q is not a mapping", ErrInvalidValue, key, section)
	}
	newValue := encodeValueNode(spec, v)
	var old *yaml.Node
	index := -1
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := resolveAliasNode(m.Content[i])
		if k != nil && k.Kind == yaml.ScalarNode && k.Value == leaf {
			index = i + 1
			old = m.Content[i+1]
			break
		}
	}
	if index >= 0 {
		oldTarget := resolveAliasNode(old)
		if oldTarget != nil {
			newValue.HeadComment = oldTarget.HeadComment
			newValue.LineComment = oldTarget.LineComment
			newValue.FootComment = oldTarget.FootComment
			if newValue.Kind == yaml.ScalarNode && oldTarget.Style&yaml.DoubleQuotedStyle != 0 {
				newValue.Style = yaml.DoubleQuotedStyle
			}
		}
		m.Content[index] = newValue
	} else {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: leaf}
		m.Content = append(m.Content, keyNode, newValue)
	}
	d.values[key] = copyValue(v)
	d.present[key] = true
	delete(d.invalid, key)
	return nil
}

func encodeValueNode(spec *keySpec, v Value) *yaml.Node {
	switch spec.Kind {
	case keyBool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: boolText(v.Bool)}
	case keyInt:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", v.Int)}
	case keyDuration:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v.Duration.String()}
	case keyStringList:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range v.Strings {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item})
		}
		return n
	default: // keyString, keySecret
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v.String}
	}
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (d *configDocument) bytes() ([]byte, error) {
	if d.root == nil {
		*d = emptyDocument()
	}
	var buf bytes.Buffer
	if d.headComment != "" {
		buf.WriteString(strings.TrimRight(d.headComment, "\n"))
		buf.WriteString("\n")
	}
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.document); err != nil {
		return nil, fmt.Errorf("%w: cannot encode config file: %v", ErrFileWrite, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("%w: cannot encode config file: %v", ErrFileWrite, err)
	}
	out := buf.Bytes()
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	if err := d.verify(out); err != nil {
		return nil, err
	}
	return out, nil
}

// verify parses the encoded bytes again, so a node edit can never produce an
// invalid structure.
func (d *configDocument) verify(data []byte) error {
	back, err := parseConfigFile(data)
	if err != nil {
		return fmt.Errorf("%w: encoded config file is invalid: %v", ErrFileWrite, err)
	}
	for name, want := range d.values {
		got, ok := back.values[name]
		if !ok {
			return fmt.Errorf("%w: encoded config file lost key %s", ErrFileWrite, name)
		}
		if !valueEqual(want, got) {
			return fmt.Errorf("%w: encoded config file changed key %s", ErrFileWrite, name)
		}
	}
	return nil
}

func valueEqual(a, b Value) bool {
	if a.Kind != b.Kind || a.String != b.String || a.Bool != b.Bool ||
		a.Int != b.Int || a.Duration != b.Duration || a.SecretIsSet != b.SecretIsSet ||
		len(a.Strings) != len(b.Strings) {
		return false
	}
	for i := range a.Strings {
		if a.Strings[i] != b.Strings[i] {
			return false
		}
	}
	return true
}

// configFileDirMode returns the mode for the directory holding the config
// file when writeConfigFile has to create it. The deployment mode fixes the
// file mode: a user Broker writes 0600 under a 0700 data directory, a system
// Broker writes 0644 under the group-readable /etc/agent-peer (0755).
func configFileDirMode(fileMode os.FileMode) os.FileMode {
	if fileMode.Perm()&0o044 != 0 {
		return 0o755
	}
	return 0o700
}

// writeConfigFile rewrites path in place: existing ownership, group, mode and
// ACLs stay in place because the same inode is reused.
func writeConfigFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s: %v", ErrFileWrite, path, err)
		}
		if mkErr := os.MkdirAll(filepath.Dir(path), configFileDirMode(mode)); mkErr != nil {
			return fmt.Errorf("%w: %s: %v", ErrFileWrite, path, mkErr)
		}
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				f, err = os.OpenFile(path, os.O_RDWR, 0)
			}
			if err != nil {
				return fmt.Errorf("%w: %s: %v", ErrFileWrite, path, err)
			}
		} else if chmodErr := f.Chmod(mode); chmodErr != nil {
			_ = f.Close()
			return fmt.Errorf("%w: %s: %v", ErrFileWrite, path, chmodErr)
		}
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrFileWrite, path, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrFileWrite, path, err)
	}
	n, err := f.Write(data)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrFileWrite, path, err)
	}
	if n != len(data) {
		return fmt.Errorf("%w: %s: short write", ErrFileWrite, path)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrFileWrite, path, err)
	}
	return nil
}
