// Package rojo ports @roblox-ts/rojo-resolver 1.1.0 and
// @roblox-ts/path-translator 1.1.0 to Go (vendored at
// reference/rojo-resolver and reference/path-translator; all line references
// are into those src/*.ts files). This code decides require paths, so it is
// byte-parity-critical downstream — quirks are ported verbatim.
package rojo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"rotor/tsgo/vfs/osvfs"
)

// Extension constants (RojoResolver.ts L7-19, constants.ts).
const (
	luaExt  = ".lua"
	luauExt = ".luau"
	jsonExt = ".json"
	tomlExt = ".toml"

	initName = "init"

	serverSubExt = ".server"
	clientSubExt = ".client"
	moduleSubExt = ""
)

var (
	rojoModuleExts = map[string]bool{luauExt: true, jsonExt: true, tomlExt: true}
	rojoScriptExts = map[string]bool{luauExt: true}
)

// Config file names (RojoResolver.ts L47-49).
var rojoFileRegex = regexp.MustCompile(`^.+\.project\.json$`)

const (
	rojoDefaultName = "default.project.json"
	rojoOldName     = "roblox-project.json"
)

// RbxType classifies an output file (RojoResolver.ts L51-56).
type RbxType int

const (
	RbxTypeModuleScript RbxType = iota
	RbxTypeScript
	RbxTypeLocalScript
	RbxTypeUnknown
)

var subExtTypeMap = map[string]RbxType{
	moduleSubExt: RbxTypeModuleScript,
	serverSubExt: RbxTypeScript,
	clientSubExt: RbxTypeLocalScript,
}

// FileRelation describes isolated-container containment of an import edge
// (RojoResolver.ts L87-92).
type FileRelation int

const (
	FileRelationOutToOut FileRelation = iota // absolute
	FileRelationOutToIn                      // error
	FileRelationInToOut                      // absolute
	FileRelationInToIn                       // relative
)

// NetworkType describes server/client-only containers (RojoResolver.ts L94-98).
type NetworkType int

const (
	NetworkTypeUnknown NetworkType = iota
	NetworkTypeClient
	NetworkTypeServer
)

// Isolated/network container tables (RojoResolver.ts L64-74).
var defaultIsolatedContainers = []RbxPath{
	{"StarterPack"},
	{"StarterGui"},
	{"StarterPlayer", "StarterPlayerScripts"},
	{"StarterPlayer", "StarterCharacterScripts"},
	{"StarterPlayer", "StarterCharacter"},
	{"PluginDebugService"},
}

var (
	clientContainers = []RbxPath{{"StarterPack"}, {"StarterGui"}, {"StarterPlayer"}}
	serverContainers = []RbxPath{{"ServerStorage"}, {"ServerScriptService"}}
)

// RbxPath is a Roblox instance tree path (RojoResolver.ts L79).
type RbxPath = []string

// RelativeRbxPathSegment is one segment of a RelativeRbxPath: either the
// Parent sentinel or an instance name (upstream string | RbxPathParent).
type RelativeRbxPathSegment struct {
	Parent bool
	Name   string
}

// RbxPathParent is the Parent sentinel segment (RojoResolver.ts L160).
var RbxPathParent = RelativeRbxPathSegment{Parent: true}

// RelativeRbxPath is the result of Relative (RojoResolver.ts L80).
type RelativeRbxPath = []RelativeRbxPathSegment

// PartitionInfo maps a filesystem subtree to an rbx path (RojoResolver.ts L82-85).
type PartitionInfo struct {
	RbxPath RbxPath
	FsPath  string
}

// ResolverFilePathState records one exact filesystem-to-Roblox mapping.
type ResolverFilePathState struct {
	Path    string  `json:"path"`
	RbxPath RbxPath `json:"rbxPath"`
}

// ResolverState is a deterministic, serializable snapshot of a RojoResolver.
// Partition order remains unchanged because it determines lookup precedence;
// exact file mappings are sorted by path because their source is a map.
type ResolverState struct {
	Warnings           []string                `json:"warnings"`
	RbxPath            RbxPath                 `json:"rbxPath"`
	Partitions         []PartitionInfo         `json:"partitions"`
	FilePaths          []ResolverFilePathState `json:"filePaths"`
	IsolatedContainers []RbxPath               `json:"isolatedContainers"`
	IsGame             bool                    `json:"isGame"`
	WalkedConfigs      []string                `json:"walkedConfigs"`
	WalkedDirectories  []string                `json:"walkedDirectories"`
}

// RojoResolver resolves filesystem paths to Roblox instance tree paths from
// a Rojo project configuration.
type RojoResolver struct {
	warnings             []string
	rbxPath              []string
	partitions           []PartitionInfo
	filePathToRbxPathMap map[string]RbxPath
	isolatedContainers   []RbxPath
	walkedConfigs        []string
	walkedDirectories    []string
	// IsGame is true when the tree declares $className "DataModel".
	IsGame bool
}

func newRojoResolver() *RojoResolver {
	return &RojoResolver{
		filePathToRbxPathMap: make(map[string]RbxPath),
		isolatedContainers:   slices.Clone(defaultIsolatedContainers),
	}
}

// FindRojoConfigFilePath finds the Rojo config for a project directory:
// default.project.json wins, else candidates by name with a warning when
// multiple exist (RojoResolver.ts L164-183). Returns "" when none is found.
// Note upstream readdirSync returns OS order; os.ReadDir sorts by name,
// which makes candidate selection deterministic.
func FindRojoConfigFilePath(projectPath string) (string, []string) {
	var warnings []string

	defaultPath := filepath.Join(projectPath, rojoDefaultName)
	if pathExists(defaultPath) {
		return defaultPath, warnings
	}

	var candidates []string
	entries, err := os.ReadDir(projectPath)
	if err == nil {
		for _, entry := range entries {
			fileName := entry.Name()
			if fileName != rojoDefaultName && (fileName == rojoOldName || rojoFileRegex.MatchString(fileName)) {
				candidates = append(candidates, filepath.Join(projectPath, fileName))
			}
		}
	}

	if len(candidates) > 1 {
		warnings = append(warnings, fmt.Sprintf("Multiple *.project.json files found, using %s", candidates[0]))
	}
	if len(candidates) == 0 {
		return "", warnings
	}
	return candidates[0], warnings
}

// GetWarnings returns warnings accumulated while parsing configs.
func (r *RojoResolver) GetWarnings() []string {
	return r.warnings
}

// GetState returns a deterministic, independent snapshot of the resolver.
func (r *RojoResolver) GetState() ResolverState {
	filePaths := make([]ResolverFilePathState, 0, len(r.filePathToRbxPathMap))
	for path, rbxPath := range r.filePathToRbxPathMap {
		filePaths = append(filePaths, ResolverFilePathState{
			Path:    path,
			RbxPath: slices.Clone(rbxPath),
		})
	}
	sort.Slice(filePaths, func(i, j int) bool {
		return filePaths[i].Path < filePaths[j].Path
	})

	partitions := make([]PartitionInfo, len(r.partitions))
	for i, partition := range r.partitions {
		partitions[i] = PartitionInfo{
			FsPath:  partition.FsPath,
			RbxPath: slices.Clone(partition.RbxPath),
		}
	}

	return ResolverState{
		Warnings:           slices.Clone(r.warnings),
		RbxPath:            slices.Clone(r.rbxPath),
		Partitions:         partitions,
		FilePaths:          filePaths,
		IsolatedContainers: cloneRbxPaths(r.isolatedContainers),
		IsGame:             r.IsGame,
		WalkedConfigs:      sortedStrings(r.walkedConfigs),
		WalkedDirectories:  sortedStrings(r.walkedDirectories),
	}
}

// FromState restores a resolver from a ResolverState snapshot.
func FromState(state ResolverState) (*RojoResolver, error) {
	if err := validateResolverState(state); err != nil {
		return nil, err
	}
	r := &RojoResolver{
		warnings:             slices.Clone(state.Warnings),
		rbxPath:              slices.Clone(state.RbxPath),
		partitions:           make([]PartitionInfo, len(state.Partitions)),
		filePathToRbxPathMap: make(map[string]RbxPath, len(state.FilePaths)),
		isolatedContainers:   cloneRbxPaths(state.IsolatedContainers),
		walkedConfigs:        sortedStrings(state.WalkedConfigs),
		walkedDirectories:    sortedStrings(state.WalkedDirectories),
		IsGame:               state.IsGame,
	}
	for i, partition := range state.Partitions {
		r.partitions[i] = PartitionInfo{
			FsPath:  partition.FsPath,
			RbxPath: slices.Clone(partition.RbxPath),
		}
	}
	for _, filePath := range state.FilePaths {
		r.filePathToRbxPathMap[filePath.Path] = slices.Clone(filePath.RbxPath)
	}
	return r, nil
}

func validateResolverState(state ResolverState) error {
	for index, partition := range state.Partitions {
		if partition.FsPath == "" {
			return fmt.Errorf("invalid resolver state: partition %d has an empty filesystem path", index)
		}
		if filepath.Clean(partition.FsPath) != partition.FsPath {
			return fmt.Errorf("invalid resolver state: partition %d has a non-canonical filesystem path %q", index, partition.FsPath)
		}
	}

	filePaths := make(map[string]struct{}, len(state.FilePaths))
	for index, filePath := range state.FilePaths {
		if filePath.Path == "" {
			return fmt.Errorf("invalid resolver state: file mapping %d has an empty path", index)
		}
		if filepath.Clean(filePath.Path) != filePath.Path {
			return fmt.Errorf("invalid resolver state: file mapping %d has a non-canonical path %q", index, filePath.Path)
		}
		if convertToLuau(filePath.Path) != filePath.Path || !rojoModuleExts[extname(filePath.Path)] {
			return fmt.Errorf("invalid resolver state: file mapping %d has an invalid Rojo module path %q", index, filePath.Path)
		}
		if _, ok := filePaths[filePath.Path]; ok {
			return fmt.Errorf("invalid resolver state: duplicate file mapping %q", filePath.Path)
		}
		filePaths[filePath.Path] = struct{}{}
	}
	return nil
}

func (r *RojoResolver) warn(str string) {
	r.warnings = append(r.warnings, str)
}

// FromPath parses the given Rojo config file (RojoResolver.ts L197-201). The
// project name is NOT pushed onto rbx paths (doNotPush).
// ParseProjectFile reads and parses a Rojo project file into its top-level instance
// name and tree (the same order-preserving parse + validation FromPath uses). It is
// exported so callers that need the raw project tree — e.g. a native instance-tree
// builder — can reuse it without constructing a resolver.
func ParseProjectFile(rojoConfigFilePath string) (name string, tree *Tree, err error) {
	data, err := os.ReadFile(rojoConfigFilePath)
	if err != nil {
		return "", nil, err
	}
	configJSON := parseJSON(data)
	if msg := validateConfig(configJSON); msg != "" {
		return "", nil, fmt.Errorf("invalid Rojo configuration: %s", msg)
	}
	name, tree = configFromJSON(configJSON)
	return name, tree, nil
}

func FromPath(rojoConfigFilePath string) *RojoResolver {
	r := newRojoResolver()
	abs, err := filepath.Abs(rojoConfigFilePath)
	if err != nil {
		abs = rojoConfigFilePath
	}
	r.parseConfig(abs, true)
	return r
}

// Synthetic creates a resolver for ProjectType.Package that forces all
// imports to be relative (RojoResolver.ts L203-211): one partition mapping
// basePath to the rbx root.
func Synthetic(basePath string) *RojoResolver {
	r := newRojoResolver()
	p := basePath
	r.parseTree(basePath, "", &Tree{Path: &p}, true)
	return r
}

// FromTree creates a resolver from an in-memory tree (RojoResolver.ts L213-217).
func FromTree(basePath string, tree *Tree) *RojoResolver {
	r := newRojoResolver()
	r.parseTree(basePath, "", tree, true)
	return r
}

// parseConfig reads + validates a project file and walks its tree
// (RojoResolver.ts L225-241). Where upstream's realpathSync would throw for
// a missing file, we fall through to the "Path does not exist" warning; a
// JSON.parse failure produces the "Invalid configuration!" warning (the
// try/finally validates undefined) and, unlike upstream, does not re-throw.
func (r *RojoResolver) parseConfig(rojoConfigFilePath string, doNotPush bool) {
	realPath := realpathOr(rojoConfigFilePath)
	r.walkConfig(realPath)
	if !pathExists(realPath) {
		r.warn(`RojoResolver: Path does not exist "` + rojoConfigFilePath + `"`)
		return
	}
	configJSON := jsonValue{kind: jsonInvalid}
	if data, err := os.ReadFile(realPath); err == nil {
		configJSON = parseJSON(data)
	}
	if msg := validateConfig(configJSON); msg != "" {
		r.warn("RojoResolver: Invalid configuration! " + msg)
		return
	}
	name, tree := configFromJSON(configJSON)
	r.parseTree(filepath.Dir(rojoConfigFilePath), name, tree, doNotPush)
}

// parseTree walks a tree node (RojoResolver.ts L243-259).
func (r *RojoResolver) parseTree(basePath, name string, tree *Tree, doNotPush bool) {
	if !doNotPush {
		r.rbxPath = append(r.rbxPath, name)
	}

	if tree.Path != nil {
		r.parsePath(resolvePath(basePath, *tree.Path))
	}

	if tree.ClassName == "DataModel" {
		r.IsGame = true
	}

	for _, child := range tree.Children {
		r.parseTree(basePath, child.Name, child.Tree, false)
	}

	if !doNotPush {
		r.rbxPath = r.rbxPath[:len(r.rbxPath)-1]
	}
}

// parsePath handles one $path target (RojoResolver.ts L261-282): module-ext
// files map exactly; directories containing default.project.json nest; all
// else becomes a partition, UNSHIFTED so later/deeper partitions match first.
func (r *RojoResolver) parsePath(itemPath string) {
	itemPath = convertToLuau(itemPath)

	// A $path that points directly at a *.project.json file composes that
	// project's tree at the current Roblox path. Its own project name is not
	// pushed, matching Rojo's nested-project mount semantics. This check must
	// precede rojoModuleExts because project files also have a .json extension.
	if rojoFileRegex.MatchString(filepath.Base(itemPath)) {
		r.parseConfig(itemPath, true)
		return
	}

	realPath := itemPath
	if pathExists(itemPath) {
		realPath = realpathOr(itemPath)
	}
	ext := extname(itemPath)
	if rojoModuleExts[ext] {
		r.filePathToRbxPathMap[itemPath] = slices.Clone(r.rbxPath)
	} else {
		isDirectory := false
		if st, err := os.Stat(realPath); err == nil && st.IsDir() {
			isDirectory = true
		}
		if isDirectory {
			r.walkDirectory(realPath)
		}
		if isDirectory && dirContains(realPath, rojoDefaultName) {
			r.parseConfig(filepath.Join(itemPath, rojoDefaultName), true)
		} else {
			r.partitions = append([]PartitionInfo{{
				FsPath:  itemPath,
				RbxPath: slices.Clone(r.rbxPath),
			}}, r.partitions...)

			if isDirectory {
				r.searchDirectory(itemPath, "")
			}
		}
	}
}

// searchDirectory recursively discovers nested project files inside a
// partition (RojoResolver.ts L284-314). A default.project.json found while
// walking is parsed WITH its name pushed (doNotPush=false), replacing the
// directory's own name.
func (r *RojoResolver) searchDirectory(directory, item string) {
	realPath := realpathOr(directory)
	r.walkDirectory(realPath)
	entries, err := os.ReadDir(realPath)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.Name() == rojoDefaultName {
			r.parseConfig(filepath.Join(directory, rojoDefaultName), false)
			return
		}
	}

	if item != "" {
		r.rbxPath = append(r.rbxPath, item)
	}

	// *.project.json (regular files). The reference stats realpathSync(child)
	// for every entry; os.ReadDir already carries the entry kind, so we only
	// fall back to a syscall for reparse points (junctions/symlinks), where
	// following the link is load-bearing for pnpm installs. Plain files/dirs —
	// the overwhelming majority of a source tree — need no extra stat.
	for _, entry := range entries {
		child := entry.Name()
		if child == rojoDefaultName || !rojoFileRegex.MatchString(child) {
			continue
		}
		if _, isRegular := entryKind(directory, entry); isRegular {
			r.parseConfig(filepath.Join(directory, child), false)
		}
	}

	// folders
	for _, entry := range entries {
		if isDir, _ := entryKind(directory, entry); isDir {
			r.searchDirectory(filepath.Join(directory, entry.Name()), entry.Name())
		}
	}

	if item != "" {
		r.rbxPath = r.rbxPath[:len(r.rbxPath)-1]
	}
}

func (r *RojoResolver) walkConfig(path string) {
	r.walkedConfigs = append(r.walkedConfigs, filepath.Clean(path))
}

func (r *RojoResolver) walkDirectory(path string) {
	r.walkedDirectories = append(r.walkedDirectories, filepath.Clean(path))
}

func cloneRbxPaths(paths []RbxPath) []RbxPath {
	cloned := make([]RbxPath, len(paths))
	for i, path := range paths {
		cloned[i] = slices.Clone(path)
	}
	return cloned
}

func sortedStrings(values []string) []string {
	if values == nil {
		return nil
	}
	sorted := slices.Clone(values)
	sort.Strings(sorted)
	return slices.Compact(sorted)
}

// entryKind reports whether a directory entry resolves to a directory or a
// regular file, matching the reference's statSync(realpathSync(child)) calls.
// os.ReadDir populates the entry kind from the directory scan itself (no extra
// syscall), so the unambiguous plain-file and plain-directory cases are decided
// for free; any reparse point (a Windows junction or symlink — the bits pnpm
// relies on) falls back to a following os.Stat so behavior is byte-identical.
func entryKind(directory string, entry os.DirEntry) (isDir, isRegular bool) {
	switch entry.Type() {
	case os.ModeDir:
		return true, false
	case 0: // regular file
		return false, true
	default:
		st, err := os.Stat(filepath.Join(directory, entry.Name()))
		if err != nil {
			return false, false
		}
		mode := st.Mode()
		return mode.IsDir(), mode.IsRegular()
	}
}

// GetRbxPathFromFilePath resolves an OUTPUT file path to its rbx path
// (RojoResolver.ts L316-337): exact map hit first, then the first matching
// partition (LIFO). Returns ok=false when no Rojo data covers the file.
func (r *RojoResolver) GetRbxPathFromFilePath(filePath string) (RbxPath, bool) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return nil, false
	}
	filePath = convertToLuau(abs)

	if rbxPath, ok := r.filePathToRbxPathMap[filePath]; ok {
		return slices.Clone(rbxPath), true
	}

	ext := extname(filePath)
	for _, partition := range r.partitions {
		if isPathDescendantOf(filePath, partition.FsPath) {
			stripped := stripRojoExts(filePath)
			relativePath, err := filepath.Rel(partition.FsPath, stripped)
			if err != nil {
				continue
			}
			var relativeParts []string
			if relativePath != "" && relativePath != "." {
				relativeParts = strings.Split(relativePath, string(filepath.Separator))
			}
			if rojoScriptExts[ext] && len(relativeParts) > 0 && relativeParts[len(relativeParts)-1] == initName {
				relativeParts = relativeParts[:len(relativeParts)-1]
			}
			result := make(RbxPath, 0, len(partition.RbxPath)+len(relativeParts))
			result = append(result, partition.RbxPath...)
			result = append(result, relativeParts...)
			return result, true
		}
	}
	return nil, false
}

// GetRbxTypeFromFilePath classifies a file by sub-extension
// (RojoResolver.ts L339-349).
func (r *RojoResolver) GetRbxTypeFromFilePath(filePath string) RbxType {
	filePath = convertToLuau(filePath)
	ext := extname(filePath)
	subext := extname(strings.TrimSuffix(filepath.Base(filePath), ext))
	if rojoScriptExts[ext] {
		if t, ok := subExtTypeMap[subext]; ok {
			return t
		}
		return RbxTypeUnknown
	}
	// non-script exts cannot use .server, .client, etc.
	return RbxTypeModuleScript
}

// getContainer returns the first container that prefixes rbxPath; only games
// have containers (RojoResolver.ts L351-361).
func (r *RojoResolver) getContainer(from []RbxPath, rbxPath RbxPath) RbxPath {
	if r.IsGame {
		for _, container := range from {
			if arrayStartsWith(rbxPath, container) {
				return container
			}
		}
	}
	return nil
}

// GetFileRelation classifies an import edge against the isolated containers
// (RojoResolver.ts L363-380).
func (r *RojoResolver) GetFileRelation(fileRbxPath, moduleRbxPath RbxPath) FileRelation {
	fileContainer := r.getContainer(r.isolatedContainers, fileRbxPath)
	moduleContainer := r.getContainer(r.isolatedContainers, moduleRbxPath)
	switch {
	case fileContainer != nil && moduleContainer != nil:
		if slices.Equal(fileContainer, moduleContainer) {
			return FileRelationInToIn
		}
		return FileRelationOutToIn
	case fileContainer != nil:
		return FileRelationInToOut
	case moduleContainer != nil:
		return FileRelationOutToIn
	default:
		return FileRelationOutToOut
	}
}

// IsIsolated reports whether rbxPath is inside an isolated container
// (RojoResolver.ts L382-384).
func (r *RojoResolver) IsIsolated(rbxPath RbxPath) bool {
	return r.getContainer(r.isolatedContainers, rbxPath) != nil
}

// GetNetworkType reports server/client-only containment; server containers
// are checked before client (RojoResolver.ts L386-394).
func (r *RojoResolver) GetNetworkType(rbxPath RbxPath) NetworkType {
	if r.getContainer(serverContainers, rbxPath) != nil {
		return NetworkTypeServer
	}
	if r.getContainer(clientContainers, rbxPath) != nil {
		return NetworkTypeClient
	}
	return NetworkTypeUnknown
}

// Relative computes the path from rbxFrom to rbxTo: one Parent per remaining
// `from` segment past the first difference, then the `to` tail
// (RojoResolver.ts L396-418).
func Relative(rbxFrom, rbxTo RbxPath) RelativeRbxPath {
	maxLength := max(len(rbxFrom), len(rbxTo))
	diffIndex := maxLength
	for i := 0; i < maxLength; i++ {
		// Out-of-range reads compare as undefined in JS: any in-range value
		// differs from it.
		if i >= len(rbxFrom) || i >= len(rbxTo) || rbxFrom[i] != rbxTo[i] {
			diffIndex = i
			break
		}
	}

	result := RelativeRbxPath{}
	if diffIndex < len(rbxFrom) {
		for i := 0; i < len(rbxFrom)-diffIndex; i++ {
			result = append(result, RbxPathParent)
		}
	}

	for i := diffIndex; i < len(rbxTo); i++ {
		result = append(result, RelativeRbxPathSegment{Name: rbxTo[i]})
	}

	return result
}

// GetPartitions exposes the partition list, front = highest precedence
// (RojoResolver.ts L420-422).
func (r *RojoResolver) GetPartitions() []PartitionInfo {
	return r.partitions
}

// --- helpers (RojoResolver.ts L100-158) ---

// stripRojoExts removes a module ext and, for script exts, a .server/.client
// sub-extension beneath it (RojoResolver.ts L100-112).
func stripRojoExts(filePath string) string {
	ext := extname(filePath)
	if rojoModuleExts[ext] {
		filePath = filePath[:len(filePath)-len(ext)]
		if rojoScriptExts[ext] {
			subext := extname(filePath)
			if subext == serverSubExt || subext == clientSubExt {
				filePath = filePath[:len(filePath)-len(subext)]
			}
		}
	}
	return filePath
}

func arrayStartsWith(a, b RbxPath) bool {
	minLength := min(len(a), len(b))
	for i := 0; i < minLength; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isPathDescendantOf mirrors RojoResolver.ts L124-126, including the quirk
// that a direct child whose name starts with ".." is treated as outside.
func isPathDescendantOf(filePath, dirPath string) bool {
	if dirPath == filePath {
		return true
	}
	rel, err := filepath.Rel(dirPath, filePath)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// convertToLuau renames a .lua path to .luau (RojoResolver.ts L154-158).
func convertToLuau(filePath string) string {
	if extname(filePath) == luaExt {
		return filePath[:len(filePath)-len(luaExt)] + luauExt
	}
	return filePath
}

// extname mirrors Node path.extname: the basename's last "."-suffix, unless
// the dot is its first character ("" for dotfiles and for "."/"..").
func extname(p string) string {
	base := filepath.Base(p)
	if base == ".." {
		return ""
	}
	idx := strings.LastIndexByte(base, '.')
	if idx <= 0 {
		return ""
	}
	return base[idx:]
}

// resolvePath mirrors Node path.resolve(basePath, p) for an absolute basePath.
func resolvePath(basePath, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(basePath, p)
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// realpathOr mirrors the reference's fs.realpathSync calls (RojoResolver.ts
// L226/263/285/298/307), falling back to the input on failure. Node's
// realpath resolves Windows junctions — pnpm's node_modules link strategy —
// but filepath.EvalSymlinks does NOT: since Go 1.23 a junction is neither a
// symlink nor a regular directory to Lstat, so EvalSymlinks fails on any
// path through one, which silently hid packages' nested default.project.json
// files from the resolver (wrong rbxPaths for pnpm installs). tsgo's osvfs
// Realpath is modeled on Node's fs.realpath.native (GetFinalPathNameByHandle
// on Windows, resolving all reparse points) and already returns the input
// unchanged on failure.
func realpathOr(p string) string {
	return filepath.FromSlash(osvfs.FS().Realpath(filepath.ToSlash(p)))
}

// dirContains mirrors fs.readdirSync(dir).includes(name): an exact,
// case-sensitive entry-name match.
func dirContains(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return true
		}
	}
	return false
}

// CanonicalFileName ports Shared/util/getCanonicalFileName.ts: identity on a
// case-sensitive filesystem, lowercase otherwise. Paths are normalized to the
// OS separator first (upstream call sites pass path.normalize output).
func CanonicalFileName(filePath string, useCaseSensitiveFileNames bool) string {
	filePath = filepath.Clean(filepath.FromSlash(filePath))
	if useCaseSensitiveFileNames {
		return filePath
	}
	return strings.ToLower(filePath)
}
