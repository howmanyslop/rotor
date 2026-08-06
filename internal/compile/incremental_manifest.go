package compile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stdjson "encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"rotor/tsgo/ast"
	"rotor/tsgo/compiler"
	"rotor/tsgo/core"
	tsjson "rotor/tsgo/json"
)

type incrementalManifest struct {
	Version int                             `json:"version"`
	Salt    string                          `json:"salt"`
	Files   map[string]incrementalFileState `json:"files"`
	Outputs map[string]string               `json:"outputs"`
}

type incrementalFileState struct {
	Hash string   `json:"hash"`
	Refs []string `json:"refs,omitempty"`
}

func readIncrementalManifest(path string) (*incrementalManifest, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest incrementalManifest
	if err := stdjson.Unmarshal(data, &manifest); err != nil {
		return nil, nil
	}
	if manifest.Version != 2 {
		return nil, nil
	}
	if manifest.Files == nil {
		manifest.Files = map[string]incrementalFileState{}
	}
	if manifest.Outputs == nil {
		manifest.Outputs = map[string]string{}
	}
	return &manifest, nil
}

func writeIncrementalManifest(path string, manifest *incrementalManifest) error {
	data, err := stdjson.MarshalIndent(manifest, "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tsbuildinfo-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func sameIncrementalManifest(a, b *incrementalManifest) bool {
	return reflect.DeepEqual(a, b)
}

func buildIncrementalManifest(program *compiler.Program, sourceFiles []*ast.SourceFile, salt string, previous *incrementalManifest) (*incrementalManifest, error) {
	manifest := &incrementalManifest{
		Version: 2,
		Salt:    salt,
		Files:   make(map[string]incrementalFileState, len(sourceFiles)),
		Outputs: map[string]string{},
	}
	currentHashes := make(map[string]string, len(sourceFiles))
	for _, sourceFile := range sourceFiles {
		path := normalizeSourceFilePath(sourceFile.FileName())
		sum := sha256.Sum256([]byte(sourceFile.Text()))
		currentHashes[path] = hex.EncodeToString(sum[:])
	}
	if previous != nil && previous.Salt == salt && len(previous.Files) == len(currentHashes) {
		unchanged := true
		for path, hash := range currentHashes {
			if prev, ok := previous.Files[path]; !ok || prev.Hash != hash {
				unchanged = false
				break
			}
		}
		if unchanged {
			// No source changed: reuse the previous reference graph verbatim,
			// so the manifest stays byte-identical and no checker work runs.
			manifest.Files = maps.Clone(previous.Files)
			return manifest, nil
		}
	}
	sourceSet := make(map[string]struct{}, len(sourceFiles))
	for _, sourceFile := range sourceFiles {
		sourceSet[normalizeSourceFilePath(sourceFile.FileName())] = struct{}{}
	}
	for _, sourceFile := range sourceFiles {
		path := normalizeSourceFilePath(sourceFile.FileName())
		refs := referencedProjectFiles(program, sourceFile, sourceSet)
		manifest.Files[path] = incrementalFileState{Hash: currentHashes[path], Refs: refs}
	}
	return manifest, nil
}

func incrementalSalt(program *compiler.Program, opts ProjectOptions, pathTranslatorBuildInfoPath string) (string, error) {
	options := program.Options()
	payload, err := tsjson.Marshal(struct {
		Version              string                `json:"version"`
		CompilerOptions      *core.CompilerOptions `json:"compilerOptions"`
		ConfigFilePath       string                `json:"configFilePath"`
		OutDir               string                `json:"outDir"`
		TsBuildInfoFile      string                `json:"tsBuildInfoFile"`
		PathTranslatorTarget string                `json:"pathTranslatorBuildInfoPath"`
		Type                 string                `json:"type"`
		RojoConfigPath       string                `json:"rojoConfigPath"`
		IncludePath          string                `json:"includePath"`
		LuaExtension         bool                  `json:"luaExtension"`
		Declaration          bool                  `json:"declaration"`
		EmitDeclarationOnly  bool                  `json:"emitDeclarationOnly"`
		NoOptimizedLoops     bool                  `json:"noOptimizedLoops"`
		MinifyOutput         bool                  `json:"minifyOutput"`
	}{
		Version:              "rotor-incremental-v2",
		CompilerOptions:      options,
		ConfigFilePath:       options.ConfigFilePath,
		OutDir:               options.OutDir,
		TsBuildInfoFile:      options.TsBuildInfoFile,
		PathTranslatorTarget: pathTranslatorBuildInfoPath,
		Type:                 string(opts.Type),
		RojoConfigPath:       opts.RojoConfigPath,
		IncludePath:          opts.IncludePath,
		LuaExtension:         !opts.LuaExtension,
		Declaration:          options.Declaration.IsTrue(),
		EmitDeclarationOnly:  opts.EmitDeclarationOnly,
		NoOptimizedLoops:     opts.NoOptimizedLoops,
		MinifyOutput:         opts.MinifyOutput,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func pruneMissingOutputs(index *outputPresenceIndex, outputs map[string]string) {
	for path := range outputs {
		if !index.hasRegular(path) {
			delete(outputs, path)
		}
	}
}

func outputManifestPath(projectDir, configPath string) string {
	canonical, err := filepath.Abs(configPath)
	if err != nil {
		canonical = filepath.Clean(configPath)
	}
	sum := sha256.Sum256([]byte(filepath.ToSlash(canonical)))
	return filepath.Join(projectDir, ".rotor", "cache", "output-manifests", hex.EncodeToString(sum[:])+".json")
}

func selectIncrementalSourceFiles(sourceFiles []*ast.SourceFile, current, previous *incrementalManifest) []*ast.SourceFile {
	if previous == nil || previous.Salt != current.Salt {
		return sourceFiles
	}

	changed := make(map[string]struct{})
	for path, state := range current.Files {
		prev, ok := previous.Files[path]
		if !ok || prev.Hash != state.Hash {
			changed[path] = struct{}{}
		}
	}
	for path := range previous.Files {
		if _, ok := current.Files[path]; !ok {
			changed[path] = struct{}{}
		}
	}
	if len(changed) == 0 {
		return nil
	}

	reverse := make(map[string][]string)
	accumulateReverseDeps(reverse, current)
	accumulateReverseDeps(reverse, previous)

	selected := make(map[string]struct{})
	queue := make([]string, 0, len(changed))
	for path := range changed {
		queue = append(queue, path)
		if _, ok := current.Files[path]; ok {
			selected[path] = struct{}{}
		}
	}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		for _, importer := range reverse[path] {
			if _, seen := selected[importer]; seen {
				continue
			}
			selected[importer] = struct{}{}
			queue = append(queue, importer)
		}
	}

	result := make([]*ast.SourceFile, 0, len(selected))
	for _, sourceFile := range sourceFiles {
		if _, ok := selected[normalizeSourceFilePath(sourceFile.FileName())]; ok {
			result = append(result, sourceFile)
		}
	}
	return result
}

func accumulateReverseDeps(reverse map[string][]string, manifest *incrementalManifest) {
	if manifest == nil {
		return
	}
	for importer, state := range manifest.Files {
		for _, dep := range state.Refs {
			reverse[dep] = append(reverse[dep], importer)
		}
	}
}

func referencedProjectFiles(program *compiler.Program, file *ast.SourceFile, sourceSet map[string]struct{}) []string {
	referenced := make(map[string]struct{})
	add := func(path string) {
		path = normalizeSourceFilePath(path)
		if path == normalizeSourceFilePath(file.FileName()) {
			return
		}
		if _, ok := sourceSet[path]; ok {
			referenced[path] = struct{}{}
		}
	}

	checker, done := program.GetTypeCheckerForFileExclusive(context.Background(), file)
	defer done()

	addSymbolDecls := func(symbol *ast.Symbol) {
		if symbol == nil {
			return
		}
		for _, declaration := range symbol.Declarations {
			if sourceFile := ast.GetSourceFileOfNode(declaration); sourceFile != nil {
				add(sourceFile.FileName())
			}
		}
	}

	for _, importName := range file.Imports() {
		addSymbolDecls(checker.GetSymbolAtLocation(importName))
	}

	sourceFileDirectory := filepath.Dir(filepath.FromSlash(file.FileName()))
	for _, referencedFile := range file.ReferencedFiles {
		add(resolveReferencedFile(program, referencedFile.FileName, sourceFileDirectory))
	}

	if typeRefsInFile, ok := program.GetResolvedTypeReferenceDirectives()[file.Path()]; ok {
		for _, typeRef := range typeRefsInFile {
			if typeRef.ResolvedFileName != "" {
				add(resolveReferencedFile(program, typeRef.ResolvedFileName, sourceFileDirectory))
			}
		}
	}

	for _, moduleName := range file.ModuleAugmentations {
		if ast.IsStringLiteral(moduleName) {
			addSymbolDecls(checker.GetSymbolAtLocation(moduleName))
		}
	}

	for _, ambientModule := range checker.GetAmbientModules() {
		addSymbolDecls(ambientModule)
	}

	refs := make([]string, 0, len(referenced))
	for path := range referenced {
		refs = append(refs, path)
	}
	sort.Strings(refs)
	return refs
}

func resolveReferencedFile(program *compiler.Program, fileName, sourceFileDirectory string) string {
	if redirect := program.GetParseFileRedirect(fileName); redirect != "" {
		return redirect
	}
	if filepath.IsAbs(filepath.FromSlash(fileName)) {
		return filepath.FromSlash(fileName)
	}
	return filepath.Join(sourceFileDirectory, filepath.FromSlash(fileName))
}

func normalizeSourceFilePath(path string) string {
	return filepath.Clean(filepath.FromSlash(path))
}
