package axconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	goccyyaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

const (
	// ProtectedModeKeys compares the protected keys one by one.
	ProtectedModeKeys = "keys"
	// ProtectedModeFile compares the whole file: the config uses forms (literal
	// dotted top-level keys, anchors, merge keys, several documents) that cannot
	// be read key by key.
	ProtectedModeFile = "file"

	// protectedSnapshotVersion is the layout of the snapshot file. A snapshot
	// with another version is reported as unreadable instead of being rebuilt.
	protectedSnapshotVersion = 1

	// protectedDefaultSnapshotName is the snapshot of SUGA_HOME/config.yaml; any
	// other SUGA_CONFIG file is named after the hash of its normalized path.
	protectedDefaultSnapshotName = "default.json"
)

// errProtectedSnapshotUnreadable marks a snapshot that exists but cannot be
// decoded. Callers report it instead of silently rebuilding the snapshot.
var errProtectedSnapshotUnreadable = errors.New("protected snapshot unreadable")

// ProtectedDigest is the state of the protected settings of one config file:
// the per-key digests (Mode "keys"), the whole-file digest, or both.
type ProtectedDigest struct {
	Mode string            // "keys" | "file"
	Keys map[string]string // config key -> digest of its raw YAML value
	File string            // digest of the whole config file
}

// ProtectedChange lists what changed since the snapshot was taken. Keys holds
// config key names only - never values, because protected keys include secrets.
type ProtectedChange struct {
	Keys               []string
	WholeFile          bool
	SnapshotUnreadable bool
}

// Empty reports whether nothing changed.
func (c ProtectedChange) Empty() bool {
	return len(c.Keys) == 0 && !c.WholeFile && !c.SnapshotUnreadable
}

// DigestProtected summarizes the protected settings of the given config bytes.
// Values are taken as written (a "${VAR}" reference is not expanded), so the
// comparison follows the file rather than the environment.
func DigestProtected(data []byte) (ProtectedDigest, error) {
	fileSum := sha256.Sum256(data)
	mode, err := protectedFileMode(data)
	if err != nil {
		return ProtectedDigest{}, err
	}
	root := map[string]any{}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := goccyyaml.Unmarshal(data, &root); err != nil {
			return ProtectedDigest{}, fmt.Errorf("parse config for protected digest: %w", err)
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	digest := ProtectedDigest{
		Mode: mode,
		Keys: protectedKeyDigests(root),
		File: hex.EncodeToString(fileSum[:]),
	}
	return digest, nil
}

// protectedFileMode reports whether the file can be compared key by key. The
// per-key digests are still computed in file mode, but they do not take part in
// the comparison.
func protectedFileMode(data []byte) (string, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return ProtectedModeKeys, nil
	}
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parse config for protected mode: %w", err)
	}
	if len(file.Docs) > 1 {
		return ProtectedModeFile, nil
	}
	if len(file.Docs) == 0 || file.Docs[0].Body == nil {
		// An empty file or one with comments only has no protected keys set.
		return ProtectedModeKeys, nil
	}
	if len(ast.FilterFile(ast.AnchorType, file)) != 0 || len(ast.FilterFile(ast.AliasType, file)) != 0 {
		return ProtectedModeFile, nil
	}
	if len(ast.FilterFile(ast.MergeKeyType, file)) != 0 {
		return ProtectedModeFile, nil
	}
	root, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		// A file with comments only parses to a comment node (or nothing) and has
		// no keys set; any other non-mapping document is not a config and cannot
		// be read key by key.
		if _, comments := file.Docs[0].Body.(*ast.CommentGroupNode); comments {
			return ProtectedModeKeys, nil
		}
		return ProtectedModeFile, nil
	}
	for _, entry := range root.Values {
		key, err := yamlKey(entry.Key)
		if err != nil {
			return ProtectedModeFile, nil
		}
		// A literal dotted top-level key ("tools.approval": auto) is read by the
		// runtime as that path, but not as a nested mapping, so the file cannot be
		// compared key by key.
		if key == "<<" || strings.Contains(key, ".") {
			return ProtectedModeFile, nil
		}
	}
	return ProtectedModeKeys, nil
}

// protectedKeyDigests digests the value of every protected registry key that
// is set in the file. Provider and model keys are enumerated from the
// providers mapping, so adding or removing a provider shows up as a change.
func protectedKeyDigests(root map[string]any) map[string]string {
	keys := make(map[string]string)
	for _, descriptor := range ProtectedConfigKeys() {
		pattern := descriptor.Pattern
		if !strings.Contains(pattern, "<") {
			if value, ok := lookupMap(root, strings.Split(pattern, ".")); ok {
				keys[pattern] = protectedValueDigest(value)
			}
			continue
		}
		collectDynamicProtectedDigests(keys, root, strings.Split(pattern, "."))
	}
	return keys
}

// collectDynamicProtectedDigests resolves one dynamic descriptor
// (providers.<id>.… or providers.<id>.models.<name>.…) against the file.
func collectDynamicProtectedDigests(out map[string]string, root map[string]any, segments []string) {
	if len(segments) < 3 || segments[0] != "providers" || segments[1] != "<id>" {
		return
	}
	rawProviders, ok := root["providers"].(map[string]any)
	if !ok {
		return
	}
	for id, rawProvider := range rawProviders {
		provider, ok := rawProvider.(map[string]any)
		if !ok {
			continue
		}
		collectProviderPatternDigests(out, id, provider, segments[2:])
	}
}

// collectProviderPatternDigests covers the provider-level and model-level
// fields of one provider.
func collectProviderPatternDigests(out map[string]string, id string, provider map[string]any, rest []string) {
	if len(rest) >= 2 && rest[0] == "models" && rest[1] == "<name>" {
		rawModels, ok := provider["models"].(map[string]any)
		if !ok {
			return
		}
		for name, rawModel := range rawModels {
			model, ok := rawModel.(map[string]any)
			if !ok {
				continue
			}
			if value, ok := lookupMap(model, rest[2:]); ok {
				key := "providers." + id + ".models." + name + "." + strings.Join(rest[2:], ".")
				out[key] = protectedValueDigest(value)
			}
		}
		return
	}
	if value, ok := lookupMap(provider, rest); ok {
		out["providers."+id+"."+strings.Join(rest, ".")] = protectedValueDigest(value)
	}
}

// protectedValueDigest serializes the parsed value into a canonical JSON form
// (mapping keys sorted, list order kept) and hashes it.
func protectedValueDigest(value any) string {
	data, err := json.Marshal(canonicalValue(value))
	if err != nil {
		data = []byte(fmt.Sprint(value))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalValue turns every mapping into map[string]any so JSON encoding sorts
// its keys the same way regardless of how YAML decoded it.
func canonicalValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = canonicalValue(item)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[fmt.Sprint(key)] = canonicalValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = canonicalValue(item)
		}
		return out
	default:
		return value
	}
}

// CompareProtected compares the current digest with the snapshot of this config
// file. A missing snapshot is created from cur and reports no change; a damaged
// one reports every current protected key as changed.
func CompareProtected(configPath string, cur ProtectedDigest) (ProtectedChange, error) {
	lock, err := acquireBootstrapLock(configLockPath(configPath))
	if err != nil {
		return ProtectedChange{}, fmt.Errorf("lock config %q: %w", configPath, err)
	}
	defer func() { _ = lock.Close() }()

	snapshotPath, err := protectedSnapshotPath(configPath)
	if err != nil {
		return ProtectedChange{}, err
	}
	snapshot, err := readProtectedSnapshot(snapshotPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := writeProtectedSnapshot(snapshotPath, cur); err != nil {
			return ProtectedChange{}, err
		}
		return ProtectedChange{}, nil
	case errors.Is(err, errProtectedSnapshotUnreadable):
		return ProtectedChange{Keys: sortedDigestKeys(cur), SnapshotUnreadable: true}, nil
	case err != nil:
		return ProtectedChange{}, err
	}

	// Whole-file comparison: either side cannot be read key by key. A snapshot
	// without a file digest cannot prove the file is unchanged.
	if cur.Mode == ProtectedModeFile || snapshot.Mode == ProtectedModeFile {
		if snapshot.File == "" || snapshot.File != cur.File {
			return ProtectedChange{WholeFile: true}, nil
		}
		return ProtectedChange{}, nil
	}

	// Union of both key sets: a key set on one side only counts as changed, so
	// adding or removing a provider or a key is reported.
	names := make(map[string]struct{}, len(cur.Keys)+len(snapshot.Keys))
	for key := range cur.Keys {
		names[key] = struct{}{}
	}
	for key := range snapshot.Keys {
		names[key] = struct{}{}
	}
	changed := make([]string, 0, len(names))
	for key := range names {
		if snapshot.Keys[key] != cur.Keys[key] {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return ProtectedChange{Keys: changed}, nil
}

// AdvanceProtected replaces the snapshot with cur. Chat calls it after the
// banner showed the change, so the next start compares against what the user
// has seen.
func AdvanceProtected(configPath string, cur ProtectedDigest) error {
	lock, err := acquireBootstrapLock(configLockPath(configPath))
	if err != nil {
		return fmt.Errorf("lock config %q: %w", configPath, err)
	}
	defer func() { _ = lock.Close() }()
	snapshotPath, err := protectedSnapshotPath(configPath)
	if err != nil {
		return err
	}
	return writeProtectedSnapshot(snapshotPath, cur)
}

// updateProtectedKeyLocked records the new digest of one key the human just
// wrote through `suga config set/unset`. The config lock is already held by
// editConfigFile (the lock is not reentrant). Nothing is done when the key is
// not protected, when there is no snapshot, or when either side cannot be
// compared key by key - those changes wait for the chat banner.
func updateProtectedKeyLocked(path, key string, data []byte) error {
	descriptor, err := MatchConfigKey(key)
	if err != nil || !descriptor.Protected {
		return nil
	}
	snapshotPath, err := protectedSnapshotPath(path)
	if err != nil {
		return err
	}
	snapshot, err := readProtectedSnapshot(snapshotPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if snapshot.Mode != ProtectedModeKeys {
		return nil
	}
	digest, err := DigestProtected(data)
	if err != nil {
		return err
	}
	if digest.Mode != ProtectedModeKeys {
		return nil
	}
	if snapshot.Keys == nil {
		snapshot.Keys = make(map[string]string)
	}
	if value, ok := digest.Keys[key]; ok {
		snapshot.Keys[key] = value
	} else {
		delete(snapshot.Keys, key)
	}
	return writeProtectedSnapshot(snapshotPath, snapshot)
}

// readProtectedSnapshot loads a snapshot file. A missing file is reported as
// os.ErrNotExist, a damaged one as errProtectedSnapshotUnreadable.
func readProtectedSnapshot(path string) (ProtectedDigest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProtectedDigest{}, err
	}
	var stored struct {
		Version int               `json:"version"`
		Mode    string            `json:"mode"`
		Keys    map[string]string `json:"keys"`
		File    string            `json:"file"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return ProtectedDigest{}, fmt.Errorf("%w: decode %q: %v", errProtectedSnapshotUnreadable, path, err)
	}
	if stored.Version != protectedSnapshotVersion {
		return ProtectedDigest{}, fmt.Errorf("%w: %q has version %d", errProtectedSnapshotUnreadable, path, stored.Version)
	}
	if stored.Mode != ProtectedModeKeys && stored.Mode != ProtectedModeFile {
		return ProtectedDigest{}, fmt.Errorf("%w: %q has mode %q", errProtectedSnapshotUnreadable, path, stored.Mode)
	}
	return ProtectedDigest{Mode: stored.Mode, Keys: stored.Keys, File: stored.File}, nil
}

// writeProtectedSnapshot writes every field of digest through a temporary file
// and a rename, so a concurrent reader sees either the old or the new snapshot.
func writeProtectedSnapshot(path string, digest ProtectedDigest) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create protected snapshot dir: %w", err)
	}
	keys := digest.Keys
	if keys == nil {
		keys = map[string]string{}
	}
	payload, err := json.Marshal(struct {
		Version int               `json:"version"`
		Mode    string            `json:"mode"`
		Keys    map[string]string `json:"keys"`
		File    string            `json:"file"`
	}{protectedSnapshotVersion, digest.Mode, keys, digest.File})
	if err != nil {
		return fmt.Errorf("encode protected snapshot: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".protected-*.json")
	if err != nil {
		return fmt.Errorf("create protected snapshot: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write protected snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write protected snapshot: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write protected snapshot: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace protected snapshot %q: %w", path, err)
	}
	return nil
}

func sortedDigestKeys(digest ProtectedDigest) []string {
	keys := make([]string, 0, len(digest.Keys))
	for key := range digest.Keys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// normalizeConfigPath is the one form used for the config lock name and the
// snapshot name: absolute, cleaned, and lower case on Windows (where the file
// system treats the same path in any case as one file).
func normalizeConfigPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

// protectedSnapshotPath locates the snapshot of a config file. Both the default
// config and an explicit SUGA_CONFIG keep it under the home's run directory;
// direct library callers without SUGA_HOME/SUGA_CONFIG keep it beside the file.
func protectedSnapshotPath(configPath string) (string, error) {
	normalized := normalizeConfigPath(configPath)
	dir := filepath.Join(filepath.Dir(normalized), "run", "protected-config")
	name := protectedSnapshotName(normalized)
	if home, err := ResolveSugaHome(); err == nil {
		homeConfig := normalizeConfigPath(filepath.Join(home, "config.yaml"))
		switch {
		case normalized == homeConfig:
			dir = filepath.Join(home, "run", "protected-config")
			name = protectedDefaultSnapshotName
		case os.Getenv("SUGA_HOME") != "" || os.Getenv("SUGA_CONFIG") != "":
			dir = filepath.Join(home, "run", "protected-config")
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create protected snapshot dir: %w", err)
	}
	return filepath.Join(dir, name), nil
}

func protectedSnapshotName(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:]) + ".json"
}
