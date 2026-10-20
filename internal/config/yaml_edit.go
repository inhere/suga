package axconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	goccyyaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// ConfigWriteOptions controls candidate validation for a config edit. The
// validator runs before replacement and again after reading the replaced file,
// so a failed semantic check cannot leave a partially accepted configuration.
type ConfigWriteOptions struct {
	Validate func([]byte) error
}

// ConfigWriteResult describes a successful read-modify-write operation.
type ConfigWriteResult struct {
	Path    string
	Backup  string
	Changed bool
}

// WriteConfigValue replaces or appends one YAML leaf while retaining the AST's
// comments and key order. Callers that own config semantics should provide a
// validator which uses the existing typed readers.
func WriteConfigValue(path, key string, value any, opts ConfigWriteOptions) (ConfigWriteResult, error) {
	return editConfigFile(path, key, opts, func(root *ast.MappingNode, segments []string) error {
		// Missing parents are written with the value as one block-style subtree
		// (tools:\n  allow_write: true), the way people write config.yaml; building
		// them one empty map at a time could only produce flow style ({...}).
		existing, depth, err := deepestMapping(root, segments[:len(segments)-1])
		if err != nil {
			return err
		}
		if depth < len(segments)-1 {
			var nested any = value
			for i := len(segments) - 1; i > depth; i-- {
				nested = map[string]any{segments[i]: nested}
			}
			entry, err := yamlMappingEntry(segments[depth], nested)
			if err != nil {
				return err
			}
			appendMappingEntry(existing, entry)
			return nil
		}
		parent := existing
		leaf := segments[len(segments)-1]
		if entry := mappingEntry(parent, leaf); entry != nil {
			replacement, err := yamlValueNode(value)
			if err != nil {
				return err
			}
			if comment := entry.Value.GetComment(); comment != nil {
				_ = replacement.SetComment(comment)
			}
			return entry.Replace(replacement)
		}
		entry, err := yamlMappingEntry(leaf, value)
		if err != nil {
			return err
		}
		appendMappingEntry(parent, entry)
		return nil
	})
}

// UnsetConfigValue removes one YAML leaf. Empty dynamic maps are removed after
// the leaf, while comments attached to a removed entry are carried to the next
// entry or the containing mapping instead of being silently discarded.
func UnsetConfigValue(path, key string, opts ConfigWriteOptions) (ConfigWriteResult, error) {
	return editConfigFile(path, key, opts, func(root *ast.MappingNode, segments []string) error {
		parent, entry, links, err := findMappingEntry(root, segments)
		if err != nil {
			return err
		}
		if entry == nil {
			return fmt.Errorf("configuration key %q is not configured", key)
		}
		removeMappingEntry(parent, entry)
		for i := len(links) - 1; i >= 0; i-- {
			link := links[i]
			if len(link.child.Values) != 0 {
				break
			}
			removeMappingEntry(link.parent, link.entry)
		}
		return nil
	})
}

func configLockPath(path string) string {
	home, err := ResolveSugaHome()
	if err != nil {
		return path + ".lock"
	}
	homeConfig := filepath.Join(home, "config.yaml")
	lockName := "config.lock"
	if filepath.Clean(path) != filepath.Clean(homeConfig) {
		digest := sha256.Sum256([]byte(path))
		lockName = "config-" + hex.EncodeToString(digest[:]) + ".lock"
	}
	lockDir := filepath.Join(home, "run", "locks")
	// Direct library tests and callers may pass an isolated config without
	// setting SUGA_HOME; keep those locks beside the isolated file, not in the
	// user's default home. CLI calls always set or resolve the real home first.
	if os.Getenv("SUGA_HOME") == "" && os.Getenv("SUGA_CONFIG") == "" && filepath.Clean(path) != filepath.Clean(homeConfig) {
		lockDir = filepath.Join(filepath.Dir(path), "run", "locks")
	}
	// Ensure direct library callers get the same lock location as the CLI. The
	// directory is private because the lock name can identify an external path.
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		digest := sha256.Sum256([]byte(path))
		return filepath.Join(filepath.Dir(path), ".config-"+hex.EncodeToString(digest[:])+".lock")
	}
	return filepath.Join(lockDir, lockName)
}

func editConfigFile(path, key string, opts ConfigWriteOptions, edit func(*ast.MappingNode, []string) error) (ConfigWriteResult, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return ConfigWriteResult{}, fmt.Errorf("resolve config path: %w", err)
	}
	segments, err := configYAMLPath(key)
	if err != nil {
		return ConfigWriteResult{}, err
	}
	lock, err := acquireBootstrapLock(configLockPath(path))
	if err != nil {
		return ConfigWriteResult{}, fmt.Errorf("lock config %q: %w", path, err)
	}
	defer func() { _ = lock.Close() }()

	original, info, err := readConfigForEdit(path)
	if err != nil {
		return ConfigWriteResult{}, err
	}
	file, root, err := parseConfigDocument(original, path)
	if err != nil {
		return ConfigWriteResult{}, err
	}
	if err := edit(root, segments); err != nil {
		return ConfigWriteResult{}, err
	}
	candidate := renderConfigDocument(file, original)
	if err := validateConfigCandidate(candidate, path, opts); err != nil {
		return ConfigWriteResult{}, err
	}

	backup, err := createConfigBackup(path, original)
	if err != nil {
		return ConfigWriteResult{}, fmt.Errorf("backup config %q: %w", path, err)
	}
	if err := atomicWriteConfig(path, candidate, info.Mode().Perm()); err != nil {
		return ConfigWriteResult{}, fmt.Errorf("replace config %q: %w", path, err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil {
		return rollbackConfig(path, original, info.Mode().Perm(), backup, fmt.Errorf("read config after write: %w", err))
	}
	if err := validateConfigCandidate(readBack, path, opts); err != nil {
		return rollbackConfig(path, original, info.Mode().Perm(), backup, fmt.Errorf("validate config after write: %w", err))
	}
	return ConfigWriteResult{Path: path, Backup: backup, Changed: true}, nil
}

func readConfigForEdit(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat config %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("config %q is not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read config %q: %w", path, err)
	}
	return content, info, nil
}

func parseConfigDocument(content []byte, path string) (*ast.File, *ast.MappingNode, error) {
	// Parse LF text: the parser keeps the \r of a CRLF comment line in the
	// comment, and rendering it back wrote "# comment\r\r\n" - an extra blank
	// line after every comment. renderConfigDocument restores CRLF from the
	// original bytes.
	content = bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
	file, err := parser.ParseBytes(content, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if len(file.Docs) != 1 || file.Docs[0].Body == nil {
		return nil, nil, fmt.Errorf("config %q must contain exactly one YAML document", path)
	}
	if len(ast.FilterFile(ast.AnchorType, file)) != 0 || len(ast.FilterFile(ast.AliasType, file)) != 0 {
		return nil, nil, fmt.Errorf("config %q must not contain YAML anchors or aliases", path)
	}
	root, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return nil, nil, fmt.Errorf("config %q must contain a mapping", path)
	}
	if err := validateMappingKeys(root); err != nil {
		return nil, nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	return file, root, nil
}

func validateMappingKeys(mapping *ast.MappingNode) error {
	seen := make(map[string]struct{}, len(mapping.Values))
	for _, entry := range mapping.Values {
		key, err := yamlKey(entry.Key)
		if err != nil {
			return err
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate YAML key %q", key)
		}
		seen[key] = struct{}{}
		if child, ok := entry.Value.(*ast.MappingNode); ok {
			if err := validateMappingKeys(child); err != nil {
				return err
			}
		} else if sequence, ok := entry.Value.(*ast.SequenceNode); ok {
			for _, item := range sequence.Values {
				if child, ok := item.(*ast.MappingNode); ok {
					if err := validateMappingKeys(child); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func configYAMLPath(key string) ([]string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("configuration key must not be empty")
	}
	if parts, _, _, _, ok := parseProviderKey(key); ok {
		_, modelName, field, nested, _ := parseProviderKey(key)
		if modelName != "" {
			segments := []string{"providers", parts[1], "models", modelName}
			if strings.Contains(field, ".") {
				segments = append(segments, strings.Split(field, ".")...)
			} else {
				segments = append(segments, field)
			}
			return segments, nil
		}
		if nested {
			return []string{"providers", parts[1], parts[2], parts[3]}, nil
		}
	}
	segments := strings.Split(key, ".")
	for _, segment := range segments {
		if segment == "" || strings.ContainsAny(segment, "{}[]\n\r") {
			return nil, fmt.Errorf("invalid configuration key %q", key)
		}
	}
	return segments, nil
}

type configMappingLink struct {
	parent *ast.MappingNode
	entry  *ast.MappingValueNode
	child  *ast.MappingNode
}

func findMappingEntry(root *ast.MappingNode, segments []string) (*ast.MappingNode, *ast.MappingValueNode, []configMappingLink, error) {
	current := root
	links := make([]configMappingLink, 0, len(segments))
	for index, segment := range segments {
		entry := mappingEntry(current, segment)
		if entry == nil {
			return current, nil, links, nil
		}
		if index == len(segments)-1 {
			return current, entry, links, nil
		}
		child, ok := entry.Value.(*ast.MappingNode)
		if !ok {
			return nil, nil, nil, fmt.Errorf("configuration path %q is not a mapping", strings.Join(segments[:index+1], "."))
		}
		links = append(links, configMappingLink{parent: current, entry: entry, child: child})
		current = child
	}
	return current, nil, links, nil
}

// deepestMapping follows segments while they name existing mappings and returns
// the last one with the number of segments it consumed.
func deepestMapping(root *ast.MappingNode, segments []string) (*ast.MappingNode, int, error) {
	current := root
	for i, segment := range segments {
		entry := mappingEntry(current, segment)
		if entry == nil {
			return current, i, nil
		}
		child, ok := entry.Value.(*ast.MappingNode)
		if !ok {
			return nil, 0, fmt.Errorf("configuration path %q is not a mapping", segment)
		}
		current = child
	}
	return current, len(segments), nil
}

func mappingEntry(mapping *ast.MappingNode, key string) *ast.MappingValueNode {
	for _, entry := range mapping.Values {
		value, err := yamlKey(entry.Key)
		if err == nil && value == key {
			return entry
		}
	}
	return nil
}

func appendMappingEntry(mapping *ast.MappingNode, entry *ast.MappingValueNode) {
	if mapping.IsFlowStyle {
		entry.SetIsFlowStyle(true)
		mapping.Values = append(mapping.Values, entry)
		return
	}
	indent := 1
	if len(mapping.Values) > 0 {
		indent = mapping.Values[0].Key.GetToken().Position.Column
	}
	entry.AddColumn(indent - 1)
	mapping.Values = append(mapping.Values, entry)
}

func yamlKey(key ast.MapKeyNode) (string, error) {
	scalar, ok := key.(ast.ScalarNode)
	if !ok {
		return "", errors.New("YAML mapping keys must be scalar strings")
	}
	value, ok := scalar.GetValue().(string)
	if !ok {
		return "", fmt.Errorf("YAML mapping key %q is not a string", key.String())
	}
	return value, nil
}

func yamlMappingEntry(key string, value any) (*ast.MappingValueNode, error) {
	data, err := goccyyaml.Marshal(map[string]any{key: value})
	if err != nil {
		return nil, fmt.Errorf("encode YAML key %q: %w", key, err)
	}
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil || len(file.Docs) != 1 {
		if err == nil {
			err = errors.New("encoded YAML has no document")
		}
		return nil, fmt.Errorf("parse encoded YAML key %q: %w", key, err)
	}
	mapping, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok || len(mapping.Values) != 1 {
		return nil, fmt.Errorf("encoded YAML key %q is not a single mapping entry", key)
	}
	return mapping.Values[0], nil
}

func yamlValueNode(value any) (ast.Node, error) {
	data, err := goccyyaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode YAML value: %w", err)
	}
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil || len(file.Docs) != 1 || file.Docs[0].Body == nil {
		if err == nil {
			err = errors.New("encoded YAML has no value")
		}
		return nil, fmt.Errorf("parse encoded YAML value: %w", err)
	}
	return file.Docs[0].Body, nil
}

func removeMappingEntry(mapping *ast.MappingNode, entry *ast.MappingValueNode) {
	index := -1
	for i, candidate := range mapping.Values {
		if candidate == entry {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	var next *ast.MappingValueNode
	if index+1 < len(mapping.Values) {
		next = mapping.Values[index+1]
	}
	carryMappingComments(mapping, entry, next)
	mapping.Values = append(mapping.Values[:index], mapping.Values[index+1:]...)
}

func carryMappingComments(mapping *ast.MappingNode, entry, next *ast.MappingValueNode) {
	comments := []*ast.CommentGroupNode{entry.GetComment(), entry.Key.GetComment(), entry.Value.GetComment(), entry.FootComment}
	if child, ok := entry.Value.(*ast.MappingNode); ok {
		comments = append(comments, child.GetComment(), child.FootComment)
	}
	for _, comment := range comments {
		if comment == nil {
			continue
		}
		if next != nil {
			if existing := next.GetComment(); existing == nil {
				_ = next.SetComment(comment)
			} else {
				existing.Comments = append(append([]*ast.CommentNode(nil), comment.Comments...), existing.Comments...)
			}
			continue
		}
		if mapping.FootComment == nil {
			mapping.FootComment = comment
		} else {
			mapping.FootComment.Comments = append(append([]*ast.CommentNode(nil), comment.Comments...), mapping.FootComment.Comments...)
		}
	}
}

func renderConfigDocument(file *ast.File, original []byte) []byte {
	text := strings.ReplaceAll(file.String(), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if !bytesHasLineEnding(original) {
		text = strings.TrimSuffix(text, "\n")
	}
	if hasCRLF(original) {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return []byte(text)
}

func bytesHasLineEnding(content []byte) bool {
	return len(content) == 0 || content[len(content)-1] == '\n' || strings.ContainsRune(string(content), '\n')
}

func hasCRLF(content []byte) bool { return strings.Contains(string(content), "\r\n") }

func validateConfigCandidate(content []byte, path string, opts ConfigWriteOptions) error {
	if _, _, err := parseConfigDocument(content, path); err != nil {
		return fmt.Errorf("validate candidate config: %w", err)
	}
	if opts.Validate != nil {
		if err := opts.Validate(content); err != nil {
			return fmt.Errorf("validate candidate config semantics: %w", err)
		}
	}
	return nil
}

var (
	configWriteTemp     = writeBootstrapTemp
	configReplaceFile   = replaceBootstrapFile
	configSyncDirectory = syncBootstrapDir
)

func createConfigBackup(path string, original []byte) (string, error) {
	backup := path + ".bak"
	if err := atomicWriteConfig(backup, original, 0o600); err != nil {
		return "", err
	}
	return backup, nil
}

func atomicWriteConfig(path string, content []byte, mode os.FileMode) error {
	previous, readErr := os.ReadFile(path)
	hadPrevious := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	tmpName, err := configWriteTemp(path, content, mode)
	if err != nil {
		return err
	}
	if err := configReplaceFile(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := configSyncDirectory(filepath.Dir(path)); err != nil {
		if restoreErr := restoreConfigFile(path, previous, mode, hadPrevious); restoreErr != nil {
			return fmt.Errorf("sync directory after replacing config: %w; restore: %v", err, restoreErr)
		}
		return err
	}
	return nil
}

func restoreConfigFile(path string, content []byte, mode os.FileMode, hadPrevious bool) error {
	if !hadPrevious {
		return os.Remove(path)
	}
	tmpName, err := configWriteTemp(path, content, mode)
	if err != nil {
		return err
	}
	if err := configReplaceFile(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := configSyncDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

func rollbackConfig(path string, original []byte, mode os.FileMode, backup string, cause error) (ConfigWriteResult, error) {
	if err := restoreConfigFile(path, original, mode, true); err != nil {
		return ConfigWriteResult{}, fmt.Errorf("%w; restore config failed: %v", cause, err)
	}
	return ConfigWriteResult{}, fmt.Errorf("%w; config restored from %s", cause, strconv.Quote(backup))
}
