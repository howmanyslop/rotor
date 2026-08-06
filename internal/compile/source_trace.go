package compile

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"rotor/internal/transformer"
	"rotor/tsgo/sourcemap"
)

type sourceTraceMap struct {
	fileName string
	text     string
	mappings []traceMapping
}

type traceMapping struct {
	generatedLine   int32
	generatedColumn int32
	sourceLine      int32
	sourceColumn    int32
}

type rawSourceMap struct {
	Version        int      `json:"version"`
	File           string   `json:"file"`
	Sources        []string `json:"sources"`
	SourcesContent []string `json:"sourcesContent"`
	Mappings       string   `json:"mappings"`
}

func newSourceTraceMap(raw, fileName, text string) (*sourceTraceMap, error) {
	var sourceMap rawSourceMap
	if err := json.Unmarshal([]byte(raw), &sourceMap); err != nil {
		return nil, fmt.Errorf("parse transformer trace map: %w", err)
	}
	if sourceMap.Version != 3 {
		return nil, fmt.Errorf("parse transformer trace map: version = %d, want 3", sourceMap.Version)
	}

	trace := &sourceTraceMap{fileName: fileName, text: text}
	// DecodeMappings preserves generated-position order, which OriginalPosition
	// relies on for its binary search below.
	decoder := sourcemap.DecodeMappings(sourceMap.Mappings)
	for mapping := range decoder.Values() {
		if !mapping.IsSourceMapping() {
			continue
		}
		generatedColumn := int(mapping.GeneratedCharacter)
		sourceColumn := int(mapping.SourceCharacter)
		if mapping.GeneratedLine < 0 || mapping.GeneratedLine > math.MaxInt32 ||
			generatedColumn < 0 || generatedColumn > math.MaxInt32 ||
			mapping.SourceLine < 0 || mapping.SourceLine > math.MaxInt32 ||
			sourceColumn < 0 || sourceColumn > math.MaxInt32 {
			return nil, errors.New("parse transformer trace map: coordinate exceeds 32-bit range")
		}
		trace.mappings = append(trace.mappings, traceMapping{
			generatedLine:   int32(mapping.GeneratedLine),
			generatedColumn: int32(generatedColumn),
			sourceLine:      int32(mapping.SourceLine),
			sourceColumn:    int32(sourceColumn),
		})
	}
	if err := decoder.Error(); err != nil {
		return nil, fmt.Errorf("parse transformer trace map mappings: %w", err)
	}
	return trace, nil
}

func (t *sourceTraceMap) OriginalSourceFileName() string { return t.fileName }

func (t *sourceTraceMap) OriginalSourceText() string { return t.text }

// diagnosticTraces carries a compile's transformer traces, keyed the way
// normalizeSourceFilePath keys everything else. It is empty for a project with
// no transformer plugins, where the compiled text IS the text on disk.
type diagnosticTraces map[string]*sourceTraceMap

// remap rewrites one diagnostic whose position indexes the sidecar's reprinted
// text so that it indexes the original file instead. generated is the text the
// position came out of — the reprinted source the overlay program parsed.
//
// Without this, every position a transformer project reports is an index into
// text no one can see: the reprint collapses blank lines and comments and adds
// the plugins' own statements, so the offset lands somewhere further down the
// file and the code frame underlines the wrong token.
func (t diagnosticTraces) remap(info DiagnosticInfo, generated string) DiagnosticInfo {
	if len(t) == 0 || info.FileName == "" {
		return info
	}
	trace := t[normalizeSourceFilePath(info.FileName)]
	if trace == nil {
		return info
	}
	line, column, ok := utf16Position(generated, info.Offset)
	if !ok {
		return info
	}
	original := trace.OriginalPosition(transformer.SourcePosition{Line: line, Column: column})
	if original == nil {
		return info
	}
	offset, ok := byteOffsetOf(trace.text, original.Line, original.Column)
	if !ok {
		return info
	}
	info.Offset = offset
	info.Line, info.Col = lineColIn(trace.text, offset)
	// The span is measured in the reprinted text; a token that survived the
	// reprint keeps its length, but nothing guarantees it fits what is left of
	// the original file.
	if remaining := len(trace.text) - offset; info.Len > remaining {
		info.Len = remaining
	}
	return info
}

// remapAll is remap over a slice of diagnostics that all came out of the same
// generated text.
func (t diagnosticTraces) remapAll(infos []DiagnosticInfo, generated string) []DiagnosticInfo {
	if len(t) == 0 {
		return infos
	}
	for i, info := range infos {
		infos[i] = t.remap(info, generated)
	}
	return infos
}

// utf16Position converts a byte offset into the 0-based line and UTF-16 column
// a source map records. Source-map columns are UTF-16 code units because the
// generator that wrote them counts JavaScript string indices.
func utf16Position(text string, offset int) (line, column int, ok bool) {
	if offset < 0 || offset > len(text) {
		return 0, 0, false
	}
	lineStart := 0
	for i := 0; i < offset; i++ {
		if text[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	for _, r := range text[lineStart:offset] {
		column++
		if r > 0xFFFF {
			column++
		}
	}
	return line, column, true
}

// byteOffsetOf is utf16Position's inverse: the byte offset of a 0-based line
// and UTF-16 column.
func byteOffsetOf(text string, line, column int) (int, bool) {
	if line < 0 || column < 0 {
		return 0, false
	}
	offset := 0
	for current := 0; current < line; current++ {
		next := strings.IndexByte(text[offset:], '\n')
		if next < 0 {
			return 0, false
		}
		offset += next + 1
	}
	units := 0
	for i, r := range text[offset:] {
		if units >= column {
			return offset + i, true
		}
		if r == '\n' {
			return 0, false
		}
		units++
		if r > 0xFFFF {
			units++
		}
	}
	if units >= column {
		return len(text), true
	}
	return 0, false
}

func (t *sourceTraceMap) OriginalPosition(position transformer.SourcePosition) *transformer.SourcePosition {
	index := sort.Search(len(t.mappings), func(index int) bool {
		mapping := t.mappings[index]
		return int(mapping.generatedLine) > position.Line ||
			int(mapping.generatedLine) == position.Line && int(mapping.generatedColumn) > position.Column
	})
	if index == 0 {
		return nil
	}
	mapping := t.mappings[index-1]
	return &transformer.SourcePosition{Line: int(mapping.sourceLine), Column: int(mapping.sourceColumn)}
}

func rewriteSourceMapWithTrace(raw string, trace *sourceTraceMap) (string, error) {
	var sourceMap rawSourceMap
	if err := json.Unmarshal([]byte(raw), &sourceMap); err != nil {
		return "", fmt.Errorf("parse Luau source map: %w", err)
	}
	if sourceMap.Version != 3 {
		return "", fmt.Errorf("parse Luau source map: version = %d, want 3", sourceMap.Version)
	}

	mappings := []emittedMapping{}
	decoder := sourcemap.DecodeMappings(sourceMap.Mappings)
	for mapping := range decoder.Values() {
		emitted := emittedMapping{
			generatedLine:   mapping.GeneratedLine,
			generatedColumn: int(mapping.GeneratedCharacter),
		}
		if mapping.IsSourceMapping() {
			original := trace.OriginalPosition(transformer.SourcePosition{
				Line:   mapping.SourceLine,
				Column: int(mapping.SourceCharacter),
			})
			if original != nil {
				emitted.sourceLine = original.Line
				emitted.sourceColumn = original.Column
				emitted.hasSource = true
			}
		}
		mappings = append(mappings, emitted)
	}
	if err := decoder.Error(); err != nil {
		return "", fmt.Errorf("parse Luau source map mappings: %w", err)
	}

	sourceMap.Sources = []string{trace.OriginalSourceFileName()}
	sourceMap.SourcesContent = []string{trace.OriginalSourceText()}
	sourceMap.Mappings = encodeEmittedMappings(mappings)
	encoded, err := json.Marshal(sourceMap)
	if err != nil {
		return "", fmt.Errorf("encode Luau source map: %w", err)
	}
	return string(encoded), nil
}

type emittedMapping struct {
	generatedLine   int
	generatedColumn int
	sourceLine      int
	sourceColumn    int
	hasSource       bool
}

func encodeEmittedMappings(mappings []emittedMapping) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var builder strings.Builder
	lastGeneratedLine := 0
	lastGeneratedColumn := 0
	lastSourceLine := 0
	lastSourceColumn := 0
	firstMappingOnLine := true

	for _, mapping := range mappings {
		for lastGeneratedLine < mapping.generatedLine {
			builder.WriteByte(';')
			lastGeneratedLine++
			lastGeneratedColumn = 0
			firstMappingOnLine = true
		}
		if !firstMappingOnLine {
			builder.WriteByte(',')
		}
		appendVLQ(&builder, mapping.generatedColumn-lastGeneratedColumn, alphabet)
		lastGeneratedColumn = mapping.generatedColumn
		if mapping.hasSource {
			appendVLQ(&builder, 0, alphabet)
			appendVLQ(&builder, mapping.sourceLine-lastSourceLine, alphabet)
			appendVLQ(&builder, mapping.sourceColumn-lastSourceColumn, alphabet)
			lastSourceLine = mapping.sourceLine
			lastSourceColumn = mapping.sourceColumn
		}
		firstMappingOnLine = false
	}
	return builder.String()
}

func appendVLQ(builder *strings.Builder, value int, alphabet string) {
	if value < 0 {
		value = (-value << 1) | 1
	} else {
		value <<= 1
	}
	for {
		digit := value & 31
		value >>= 5
		if value > 0 {
			digit |= 32
		}
		builder.WriteByte(alphabet[digit])
		if value == 0 {
			return
		}
	}
}
