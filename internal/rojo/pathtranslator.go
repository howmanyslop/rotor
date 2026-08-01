// PathTranslator port of @roblox-ts/path-translator 1.1.0
// (reference/path-translator/src/PathTranslator.ts; line references are into
// that file). Pure path/string code — no filesystem access.
package rojo

import (
	"path/filepath"
	"strings"
)

// Extension/name constants (path-translator src/constants.ts).
const (
	tsExt          = ".ts"
	tsxExt         = ".tsx"
	dExt           = ".d"
	dtsExt         = dExt + tsExt
	transformedExt = ".transformed"
	buildInfoExt   = ".tsbuildinfo"
	rbxtscInfix    = ".rbxtsc"

	indexName = "index"
)

// pathInfo models a path as fileName + a stack of ALL dot-extensions, so
// "a.spec.ts" has exts [".spec", ".ts"] (PathTranslator.ts L16-39).
type pathInfo struct {
	dirName  string
	fileName string
	exts     []string
}

func pathInfoFrom(filePath string) *pathInfo {
	dirName := filepath.Dir(filePath)
	// Upstream slices past dirName.length + 1 unconditionally; replicate
	// (including the quirk of eating a character when dirName is not a
	// strict "<dir><sep>" prefix, e.g. bare relative file names).
	rest := ""
	if start := len(dirName) + 1; start < len(filePath) {
		rest = filePath[start:]
	}
	parts := strings.Split(rest, ".")
	exts := make([]string, 0, len(parts)-1)
	for _, v := range parts[1:] {
		exts = append(exts, "."+v)
	}
	return &pathInfo{dirName: dirName, fileName: parts[0], exts: exts}
}

// extsPeek returns the ext `depth` entries from the top of the stack, "" when
// out of range (JS undefined).
func (p *pathInfo) extsPeek(depth int) string {
	i := len(p.exts) - (depth + 1)
	if i < 0 {
		return ""
	}
	return p.exts[i]
}

func (p *pathInfo) popExt() string {
	ext := p.exts[len(p.exts)-1]
	p.exts = p.exts[:len(p.exts)-1]
	return ext
}

func (p *pathInfo) pushExt(ext string) {
	p.exts = append(p.exts, ext)
}

func (p *pathInfo) join() string {
	return filepath.Join(p.dirName, p.fileName+strings.Join(p.exts, ""))
}

// PathTranslator maps between input (.ts) and output (.luau) paths
// (PathTranslator.ts L41-220).
type PathTranslator struct {
	RootDir             string
	OutDir              string
	BuildInfoOutputPath string
	Declaration         bool
	UseLuauExtension    bool
}

// NewPathTranslator mirrors the upstream constructor. buildInfoOutputPath may
// be "" (upstream undefined).
func NewPathTranslator(rootDir, outDir, buildInfoOutputPath string, declaration, useLuauExtension bool) *PathTranslator {
	return &PathTranslator{
		RootDir:             rootDir,
		OutDir:              outDir,
		BuildInfoOutputPath: addRbxtscBuildInfoInfix(buildInfoOutputPath),
		Declaration:         declaration,
		UseLuauExtension:    useLuauExtension,
	}
}

func addRbxtscBuildInfoInfix(filePath string) string {
	if !strings.HasSuffix(filePath, buildInfoExt) {
		return filePath
	}
	withoutExt := strings.TrimSuffix(filePath, buildInfoExt)
	if strings.HasSuffix(withoutExt, rbxtscInfix) {
		return filePath
	}
	return withoutExt + rbxtscInfix + buildInfoExt
}

func (pt *PathTranslator) getLuauExt() string {
	if pt.UseLuauExtension {
		return luauExt
	}
	return luaExt
}

// makeRelative rebases pathInfo from one root into another
// (makeRelativeFactory, PathTranslator.ts L54-56).
func makeRelative(from, to string, p *pathInfo) string {
	joined := p.join()
	rel, err := filepath.Rel(from, joined)
	if err != nil {
		// Node path.relative returns the target itself when no relative
		// path exists (e.g. different drives); join then concatenates.
		rel = joined
	}
	return filepath.Join(to, rel)
}

// GetOutputPath maps an input path to an output path (PathTranslator.ts
// L58-80):
//   - `.ts(x)` && !`.d.ts(x)` -> `.lua(u)`, with `index` -> `init`
//   - `src/*` -> `out/*`
func (pt *PathTranslator) GetOutputPath(filePath string) string {
	p := pathInfoFrom(filePath)

	if (p.extsPeek(0) == tsExt || p.extsPeek(0) == tsxExt) && p.extsPeek(1) != dExt {
		p.popExt() // pop .ts(x)

		// index -> init
		if p.fileName == indexName {
			p.fileName = initName
		}

		p.pushExt(pt.getLuauExt())
	}

	return makeRelative(pt.RootDir, pt.OutDir, p)
}

// GetOutputDeclarationPath maps an input path to an output .d.ts path
// (PathTranslator.ts L82-97).
func (pt *PathTranslator) GetOutputDeclarationPath(filePath string) string {
	p := pathInfoFrom(filePath)

	if (p.extsPeek(0) == tsExt || p.extsPeek(0) == tsxExt) && p.extsPeek(1) != dExt {
		p.popExt() // pop .ts(x)
		p.pushExt(dtsExt)
	}

	return makeRelative(pt.RootDir, pt.OutDir, p)
}

// GetOutputTransformedPath maps an input path to an output
// .transformed.ts(x) path (PathTranslator.ts L99-118).
func (pt *PathTranslator) GetOutputTransformedPath(filePath string) string {
	p := pathInfoFrom(filePath)

	at := len(p.exts) - 1
	if p.extsPeek(1) == dExt {
		// Transformers currently never get a chance to transform .d.ts
		// files, but the case is covered anyway.
		at = len(p.exts) - 2
	}
	if at < 0 {
		at = 0 // JS splice clamps negative indices
	}
	p.exts = append(p.exts[:at], append([]string{transformedExt}, p.exts[at:]...)...)

	return makeRelative(pt.RootDir, pt.OutDir, p)
}

// GetInputPaths maps an output path back to its possible input paths
// (PathTranslator.ts L120-189): `.lua(u)` -> `.ts(x)` (with `init` ->
// `index`), `.d.ts(x)` -> `.ts(x)` when declaration, plus identity.
func (pt *PathTranslator) GetInputPaths(filePath string) []string {
	var possiblePaths []string
	p := pathInfoFrom(filePath)

	// index.*.lua(u) cannot come from a .ts file
	if p.extsPeek(0) == pt.getLuauExt() && p.fileName != indexName {
		p.popExt() // pop .lua(u)

		// ts
		p.pushExt(tsExt)
		possiblePaths = append(possiblePaths, makeRelative(pt.OutDir, pt.RootDir, p))
		p.popExt()

		// tsx
		p.pushExt(tsxExt)
		possiblePaths = append(possiblePaths, makeRelative(pt.OutDir, pt.RootDir, p))
		p.popExt()

		// init -> index
		if p.fileName == initName {
			originalFileName := p.fileName
			p.fileName = indexName

			// index.*.ts
			p.pushExt(tsExt)
			possiblePaths = append(possiblePaths, makeRelative(pt.OutDir, pt.RootDir, p))
			p.popExt()

			// index.*.tsx
			p.pushExt(tsxExt)
			possiblePaths = append(possiblePaths, makeRelative(pt.OutDir, pt.RootDir, p))
			p.popExt()

			p.fileName = originalFileName
		}

		p.pushExt(pt.getLuauExt())
	}

	if pt.Declaration {
		if (p.extsPeek(0) == tsExt || p.extsPeek(0) == tsxExt) && p.extsPeek(1) == dExt {
			tsLikeExt := p.popExt() // pop .ts(x)
			p.popExt()              // pop .d

			// .ts
			p.pushExt(tsExt)
			possiblePaths = append(possiblePaths, makeRelative(pt.OutDir, pt.RootDir, p))
			p.popExt()

			// .tsx
			p.pushExt(tsxExt)
			possiblePaths = append(possiblePaths, makeRelative(pt.OutDir, pt.RootDir, p))
			p.popExt()

			p.pushExt(dExt)
			p.pushExt(tsLikeExt)
		}
	}

	possiblePaths = append(possiblePaths, makeRelative(pt.OutDir, pt.RootDir, p))
	return possiblePaths
}

// GetImportPath maps a src path to an import path (PathTranslator.ts
// L191-219). Import paths are passed to RojoResolver and used virtually to
// resolve RbxPaths, so the result may not actually exist on disk.
//   - `.d.ts(x)` -> `.ts(x)` -> `.lua(u)`, with `index` -> `init`
//
// When isNodeModule is true the compiled file is assumed to be a SIBLING of
// filePath — no rootDir -> outDir rebase (node_modules d.ts files were
// already remapped to the shipped .lua location via nodeModulesPathMapping).
func (pt *PathTranslator) GetImportPath(filePath string, isNodeModule bool) string {
	p := pathInfoFrom(filePath)

	if p.extsPeek(0) == tsExt || p.extsPeek(0) == tsxExt {
		p.popExt() // pop .ts(x)
		if p.extsPeek(0) == dExt {
			p.popExt() // pop .d
		}

		// index -> init
		if p.fileName == indexName {
			p.fileName = initName
		}

		p.pushExt(pt.getLuauExt()) // push .lua(u)
	}

	if isNodeModule {
		return p.join()
	}
	return makeRelative(pt.RootDir, pt.OutDir, p)
}
