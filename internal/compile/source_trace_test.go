package compile

import (
	"reflect"
	"testing"

	"rotor/internal/transformer"
)

func TestNewSourceTraceMapDecodesMappingsInGeneratedPositionOrder(t *testing.T) {
	// Given
	raw := `{"version":3,"file":"transformed.ts","sources":["original.ts"],"sourcesContent":["first\nsecond\n"],"mappings":"AAAA,KAAI;AACJ,Q"}`

	// When
	trace, err := newSourceTraceMap(raw, "original.ts", "first\nsecond\n")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	want := []traceMapping{
		{generatedLine: 0, generatedColumn: 0, sourceLine: 0, sourceColumn: 0},
		{generatedLine: 0, generatedColumn: 5, sourceLine: 0, sourceColumn: 4},
		{generatedLine: 1, generatedColumn: 0, sourceLine: 1, sourceColumn: 0},
	}
	if !reflect.DeepEqual(trace.mappings, want) {
		t.Fatalf("decoded mappings = %#v, want %#v", trace.mappings, want)
	}
	for index := 1; index < len(trace.mappings); index++ {
		previous := trace.mappings[index-1]
		current := trace.mappings[index]
		if current.generatedLine < previous.generatedLine || current.generatedLine == previous.generatedLine && current.generatedColumn < previous.generatedColumn {
			t.Fatalf("mappings are not ordered for sort.Search: %#v", trace.mappings)
		}
	}
}

func TestSourceTraceMapOriginalPositionUsesPrecedingGeneratedMapping(t *testing.T) {
	trace := &sourceTraceMap{mappings: []traceMapping{
		{generatedLine: 0, generatedColumn: 0, sourceLine: 0, sourceColumn: 0},
		{generatedLine: 0, generatedColumn: 5, sourceLine: 0, sourceColumn: 4},
		{generatedLine: 1, generatedColumn: 0, sourceLine: 1, sourceColumn: 0},
	}}

	tests := []struct {
		name  string
		input transformer.SourcePosition
		want  *transformer.SourcePosition
	}{
		{
			name:  "before first mapping",
			input: transformer.SourcePosition{Line: 0, Column: -1},
		},
		{
			name:  "between mappings",
			input: transformer.SourcePosition{Line: 0, Column: 3},
			want:  &transformer.SourcePosition{Line: 0, Column: 0},
		},
		{
			name:  "at mapping",
			input: transformer.SourcePosition{Line: 0, Column: 5},
			want:  &transformer.SourcePosition{Line: 0, Column: 4},
		},
		{
			name:  "next generated line",
			input: transformer.SourcePosition{Line: 1, Column: 0},
			want:  &transformer.SourcePosition{Line: 1, Column: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got := trace.OriginalPosition(tt.input)

			// Then
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("OriginalPosition(%+v) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEncodeEmittedMappingsUsesStableVLQ(t *testing.T) {
	// Given
	mappings := []emittedMapping{
		{generatedLine: 0, generatedColumn: 0, sourceLine: 0, sourceColumn: 0, hasSource: true},
		{generatedLine: 0, generatedColumn: 5, sourceLine: 0, sourceColumn: 4, hasSource: true},
		{generatedLine: 1, generatedColumn: 0, sourceLine: 1, sourceColumn: 0, hasSource: true},
		{generatedLine: 1, generatedColumn: 8},
	}

	// When
	got := encodeEmittedMappings(mappings)

	// Then
	if got != "AAAA,KAAI;AACJ,Q" {
		t.Fatalf("encoded mappings = %q, want %q", got, "AAAA,KAAI;AACJ,Q")
	}
}
