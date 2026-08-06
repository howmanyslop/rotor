package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// knownPins mirrors init.go's loose pins for packages whose major has been
// stable long enough to range on (@rbxts/services 1.x, @rbxts/net v3);
// everything else gets "*" so the package manager resolves the real latest.
var knownPins = map[string]string{
	"@rbxts/services": "^1.0.0",
	"@rbxts/net":      "^3.0.0",
}

// newAddCommand adds dependency entries to the project's package.json — no
// network, no install. Each <pkg> lands in "dependencies" (or
// "devDependencies" with --dev) at a loose pin ("*", or a known stable range
// for a few packages, mirroring the init wizard). An explicit version is
// accepted as `name@range`. Existing entries are left untouched (deduped).
// The file is re-emitted with its detected indentation (2-space default) and
// a trailing newline; key order is preserved by editing the raw JSON object
// in place rather than round-tripping through a Go struct.
func newAddCommand(streams cliStreams) *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:                   "add [--dev] <pkg>...",
		Short:                 "add @rbxts/* (or any) deps to package.json",
		Args:                  cobra.MinimumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, argv []string) error {
			return runAddCommand(streams, dev, argv)
		},
	}
	cmd.Flags().SortFlags = false
	addBoolFlag(cmd, &dev, "dev", "D", false, "add to devDependencies instead of dependencies")
	return cmd
}

func runAddCommand(streams cliStreams, dev bool, pkgs []string) error {
	dir := "."
	start := time.Now()

	pkgJSONPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return runtimeFailure(fmt.Errorf("no package.json in %s (run `sloptor init` first)", absOrSelf(dir)))
	}

	depKey := "dependencies"
	if dev {
		depKey = "devDependencies"
	}

	updated, added, skipped, err := addDependencies(data, depKey, pkgs)
	if err != nil {
		return runtimeFailure(err)
	}

	out := newUI(streams.out)
	out.banner("add")

	if len(added) == 0 {
		out.events([]uiEvent{
			{
				Status: eventUnchanged,
				Target: depKey,
				Detail: fmt.Sprintf("all %s already present — nothing to add", plural(len(skipped), "package")),
			},
			{Status: eventFinished, Elapsed: time.Since(start)},
		})
		fmt.Fprintln(streams.out)
		return nil
	}

	if err := os.WriteFile(pkgJSONPath, updated, 0o644); err != nil {
		return runtimeFailure(fmt.Errorf("cannot write package.json: %w", err))
	}

	events := make([]uiEvent, 0, len(added)+1)
	for _, a := range added {
		events = append(events, uiEvent{Status: eventUpdated, Target: a, Detail: depKey})
	}
	pm := detectPackageManager(dir)
	events = append(events, uiEvent{
		Status:  eventFinished,
		Target:  fmt.Sprintf("added %s to %s", plural(len(added), "package"), depKey),
		Detail:  fmt.Sprintf("run `%s install` to fetch them", pm),
		Elapsed: time.Since(start),
	})
	out.events(events)
	fmt.Fprintln(streams.out)
	return nil
}

// addDependencies inserts each pkg into the depKey map of the raw package.json
// bytes, returning the re-marshaled file, the "name@version" entries actually
// added, and the names skipped because they were already present. Key order of
// existing fields is preserved; new keys are appended.
func addDependencies(data []byte, depKey string, pkgs []string) (updated []byte, added, skipped []string, err error) {
	indent := detectIndent(data)

	// Decode into an ordered map so existing key order survives the round-trip.
	root := newOrderedMap()
	if err := json.Unmarshal(data, root); err != nil {
		return nil, nil, nil, fmt.Errorf("package.json is not valid JSON: %w", err)
	}

	deps, _ := root.get(depKey).(*orderedMap)
	if deps == nil {
		deps = newOrderedMap()
		root.set(depKey, deps)
	}

	for _, p := range pkgs {
		name, version := splitPkgSpec(p)
		if _, ok := deps.lookup(name); ok {
			skipped = append(skipped, name)
			continue
		}
		deps.set(name, version)
		added = append(added, name+"@"+version)
	}

	if len(added) == 0 {
		return data, added, skipped, nil
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(root); err != nil {
		return nil, nil, nil, err
	}
	// json.Encoder already appends a trailing newline.
	return buf.Bytes(), added, skipped, nil
}

// splitPkgSpec splits a `name@range` spec into (name, range), defaulting the
// range to a known stable pin or "*". A leading scope `@rbxts/...` is not a
// version separator — only an `@` after the first character is.
func splitPkgSpec(spec string) (name, version string) {
	if at := strings.LastIndexByte(spec, '@'); at > 0 {
		return spec[:at], spec[at+1:]
	}
	if pin, ok := knownPins[spec]; ok {
		return spec, pin
	}
	return spec, "*"
}

// detectIndent sniffs the indentation unit of a JSON document (the whitespace
// before the first nested key), defaulting to two spaces.
func detectIndent(data []byte) string {
	s := string(data)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		rest := s[i+1:]
		ws := rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
		if ws != "" {
			return ws
		}
	}
	return "  "
}

func absOrSelf(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// --- ordered map (preserves package.json key order on round-trip) ---

// orderedMap is a minimal insertion-ordered JSON object. It implements
// json.Unmarshaler/Marshaler so nested objects (notably the dependency maps)
// keep their on-disk order; scalar values are kept as json.RawMessage so they
// re-emit byte-for-byte.
type orderedMap struct {
	keys   []string
	values map[string]any
}

func newOrderedMap() *orderedMap {
	return &orderedMap{values: map[string]any{}}
}

func (m *orderedMap) get(key string) any { return m.values[key] }

func (m *orderedMap) lookup(key string) (any, bool) {
	v, ok := m.values[key]
	return v, ok
}

func (m *orderedMap) set(key string, value any) {
	if _, ok := m.values[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

func (m *orderedMap) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("expected JSON object")
	}
	m.values = map[string]any{}
	m.keys = nil
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key := keyTok.(string)
		raw, err := decodeOrderedValue(dec)
		if err != nil {
			return err
		}
		m.set(key, raw)
	}
	// consume closing '}'
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

// decodeOrderedValue reads the next value, recursing into nested objects as
// orderedMaps and capturing everything else as raw JSON so it re-emits
// unchanged.
func decodeOrderedValue(dec *json.Decoder) (any, error) {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		nested := newOrderedMap()
		if err := json.Unmarshal(raw, nested); err != nil {
			return nil, err
		}
		return nested, nil
	}
	return raw, nil
}

func (m *orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range m.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := marshalNoEscapeHTML(key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		var valJSON []byte
		switch v := m.values[key].(type) {
		case *orderedMap:
			valJSON, err = v.MarshalJSON()
		case json.RawMessage:
			// Already-valid JSON parsed verbatim from the source file; write
			// it as-is so existing values (e.g. ">=18" in engines) keep their
			// literal characters instead of being \uXXXX-escaped.
			valJSON = v
		default:
			valJSON, err = marshalNoEscapeHTML(v)
		}
		if err != nil {
			return nil, err
		}
		buf.Write(valJSON)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// marshalNoEscapeHTML is json.Marshal without HTML escaping (so >, <, & stay
// literal — package.json regularly carries ">=18" engines and URLs with &).
func marshalNoEscapeHTML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
