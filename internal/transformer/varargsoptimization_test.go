package transformer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rotor/tsgo/ast"
	"rotor/tsgo/bundled"
	"rotor/tsgo/compiler"
	"rotor/tsgo/tsoptions"
	"rotor/tsgo/vfs/osvfs"
)

func buildVarArgsOptimizationState(t *testing.T, source string) *State {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tsconfig.json")
	sourcePath := filepath.Join(dir, "input.ts")
	if err := os.WriteFile(configPath, []byte(`{"compilerOptions":{"target":"ESNext"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	fs := bundled.WrapFS(osvfs.FS())
	host := compiler.NewCompilerHost(filepath.ToSlash(dir), fs, bundled.LibPath(), nil, nil)
	parsed, configDiags := tsoptions.GetParsedCommandLineOfConfigFile(filepath.ToSlash(configPath), nil, nil, host, nil)
	if len(configDiags) != 0 {
		t.Fatalf("config diagnostics: %v", configDiags)
	}

	program := compiler.NewProgram(compiler.ProgramOptions{Host: host, Config: parsed})
	ctx := context.Background()
	checker, release := program.GetTypeChecker(ctx)
	t.Cleanup(release)

	sourceFile := program.GetSourceFile(filepath.ToSlash(sourcePath))
	if sourceFile == nil {
		t.Fatalf("source file not in program: %s", sourcePath)
	}
	return NewState(program, checker, sourceFile, NewDiagService(), NewMultiState())
}

func varArgsOptimizationNodes(t *testing.T, s *State) (*ast.Node, *ast.Node) {
	t.Helper()

	for _, node := range s.SourceFile.Statements.Nodes {
		if !ast.IsFunctionDeclaration(node) || node.Body() == nil {
			continue
		}
		for _, parameter := range node.Parameters() {
			if parameter.AsParameterDeclaration().DotDotDotToken != nil {
				return parameter.AsNode(), node
			}
		}
	}
	t.Fatal("no function with a rest parameter")
	return nil, nil
}

func TestVarArgsOptimizationAnalysis(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		parameterIndex   int
		sizeAccessCount  int
		forOfAccessCount int
		isOptimizable    bool
	}{
		{
			name:           "safe indexed read",
			source:         "function foo(first: any, ...args: any[]) { return args[0]; }",
			parameterIndex: 1,
			isOptimizable:  true,
		},
		{
			name:          "safe indexed read through type assertion",
			source:        "function foo(...args: any[]) { return (args as any)[0]; }",
			isOptimizable: true,
		},
		{
			name:            "safe size call",
			source:          "function foo(...args: any[]) { return args.size(); }",
			sizeAccessCount: 1,
			isOptimizable:   true,
		},
		{
			name:             "safe plain identifier for of",
			source:           "function foo(...args: any[]) { for (const value of args) { value; } }",
			forOfAccessCount: 1,
			isOptimizable:    true,
		},
		{
			name:          "safe call spread",
			source:        "declare function bar(...values: any[]): void; function foo(...args: any[]) { bar(...args); }",
			isOptimizable: true,
		},
		{
			name:          "unsafe nested function capture",
			source:        "function foo(...args: any[]) { return function() { return args[0]; }; }",
			isOptimizable: false,
		},
		{
			name:          "unsafe try block use",
			source:        "function foo(...args: any[]) { try { return args[0]; } catch {} }",
			isOptimizable: false,
		},
		{
			name:          "unsafe element assignment",
			source:        "function foo(...args: any[]) { args[0] = 1; }",
			isOptimizable: false,
		},
		{
			name:          "unsafe alias",
			source:        "function foo(...args: any[]) { const alias = args; return alias; }",
			isOptimizable: false,
		},
		{
			name:          "unsafe passing as an argument",
			source:        "declare function consume(value: any[]): void; function foo(...args: any[]) { consume(args); }",
			isOptimizable: false,
		},
		{
			name:          "unsafe returning the parameter",
			source:        "function foo(...args: any[]) { return args; }",
			isOptimizable: false,
		},
		{
			name:          "unsafe property access",
			source:        "function foo(...args: any[]) { return args.map; }",
			isOptimizable: false,
		},
		{
			name:          "unsafe array literal spread",
			source:        "function foo(...args: any[]) { return [...args]; }",
			isOptimizable: false,
		},
		{
			name:           "unsafe array literal spread with other elements",
			source:         "function foo(x: any[], ...args: any[]) { return [...x, ...args]; }",
			parameterIndex: 1,
			isOptimizable:  false,
		},
		{
			name:          "unsafe destructuring for of",
			source:        "function foo(...args: any[]) { for (const [value] of args) { value; } }",
			isOptimizable: false,
		},
		{
			name:          "unsafe generator",
			source:        "function* foo(...args: any[]) { yield args[0]; }",
			isOptimizable: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := buildVarArgsOptimizationState(t, test.source)
			parameter, functionNode := varArgsOptimizationNodes(t, s)

			got := analyzeVarArgsOptimization(s, parameter, functionNode)
			if got.ParameterIndex != test.parameterIndex {
				t.Errorf("ParameterIndex = %d, want %d", got.ParameterIndex, test.parameterIndex)
			}
			if got.SizeAccessCount != test.sizeAccessCount {
				t.Errorf("SizeAccessCount = %d, want %d", got.SizeAccessCount, test.sizeAccessCount)
			}
			if got.ForOfAccessCount != test.forOfAccessCount {
				t.Errorf("ForOfAccessCount = %d, want %d", got.ForOfAccessCount, test.forOfAccessCount)
			}
			if got.IsOptimizable != test.isOptimizable {
				t.Errorf("IsOptimizable = %t, want %t", got.IsOptimizable, test.isOptimizable)
			}
		})
	}
}

func TestOptimizableVarArgsRegistration(t *testing.T) {
	s := buildVarArgsOptimizationState(t, "function foo(...args: any[]) { return args[0]; }")
	parameter, functionNode := varArgsOptimizationNodes(t, s)
	data := analyzeVarArgsOptimization(s, parameter, functionNode)
	if !data.IsOptimizable {
		t.Fatal("expected indexed rest parameter to be optimizable")
	}

	s.registerOptimizableVarArgs(parameter, data)
	if got := s.getOptimizableVarArgsData(parameter.AsParameterDeclaration().Name()); got != data {
		t.Errorf("registered data = %p, want %p", got, data)
	}

	s.unregisterOptimizableVarArgs(parameter)
	if got := s.getOptimizableVarArgsData(parameter.AsParameterDeclaration().Name()); got != nil {
		t.Errorf("unregistered data = %p, want nil", got)
	}
}

func TestTransformParametersAnalyzesRestParameter(t *testing.T) {
	s := buildVarArgsOptimizationState(t, "function foo(...args: any[]) { return args.size(); }")
	_, functionNode := varArgsOptimizationNodes(t, s)

	_, _, _, data := transformParameters(s, functionNode)
	if data == nil || !data.IsOptimizable {
		t.Fatal("transformParameters did not retain optimizable rest parameter data")
	}
	if data.SizeAccessCount != 1 {
		t.Errorf("SizeAccessCount = %d, want 1", data.SizeAccessCount)
	}
}
