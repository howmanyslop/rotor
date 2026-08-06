package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type RuntimeTools struct {
	Rojo string
	Lune string
}

var DisabledBehavioralFixtures = map[string]string{}

func detectRuntimeTools() RuntimeTools {
	return RuntimeTools{
		Rojo: detectRuntimeTool("ROTOR_ROJO_PATH", "rojo"),
		Lune: detectRuntimeTool("ROTOR_LUNE_PATH", "lune"),
	}
}

func detectRuntimeTool(envKey, binary string) string {
	if override := strings.TrimSpace(os.Getenv(envKey)); override != "" {
		if info, err := os.Stat(override); err == nil && !info.IsDir() {
			return override
		}
		return ""
	}
	return lookPath(binary)
}

func lookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

func runtimeSuiteSkipReason(tools RuntimeTools) string {
	var missing []string
	var hints []string
	if tools.Rojo == "" {
		missing = append(missing, "rojo")
		hints = append(hints, "set ROTOR_ROJO_PATH to a rojo executable or add rojo to PATH")
	}
	if tools.Lune == "" {
		missing = append(missing, "lune")
		hints = append(hints, "set ROTOR_LUNE_PATH to a lune executable or add lune to PATH")
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("runtime suite requires %s; %s", strings.Join(missing, " and "), strings.Join(hints, "; "))
}

func runtimeSuiteSourceRels(projectDir string) ([]string, error) {
	rels := []string{
		"main.server.ts",
		"services.d.ts",
	}
	for _, goldenRel := range EnabledFixtures {
		if !strings.HasPrefix(goldenRel, "tests/") {
			continue
		}
		if _, disabled := DisabledBehavioralFixtures[goldenRel]; disabled {
			continue
		}
		sourceRel, err := sourceRelFromGolden(projectDir, goldenRel)
		if err != nil {
			return nil, err
		}
		rels = append(rels, sourceRel)
	}
	slices.Sort(rels)
	return rels, nil
}

func stageRuntimeSuiteProject(baseProjectDir string) (string, error) {
	tmpProj, err := os.MkdirTemp(baseProjectDir, ".phase5-runtime-")
	if err != nil {
		return "", err
	}
	if err := copyFile(filepath.Join(baseProjectDir, "package.json"), filepath.Join(tmpProj, "package.json")); err != nil {
		return "", err
	}
	if err := copyFile(filepath.Join(baseProjectDir, "tsconfig.json"), filepath.Join(tmpProj, "tsconfig.json")); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmpProj, "default.project.json"), []byte(runtimeRojoConfig()), 0o644); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(tmpProj, "src"), 0o755); err != nil {
		return "", err
	}
	if err := stageRuntimeTypeRoots(baseProjectDir, tmpProj); err != nil {
		return "", err
	}
	if err := copyTree(filepath.Join(baseProjectDir, "src", "helpers"), filepath.Join(tmpProj, "src", "helpers")); err != nil {
		return "", err
	}

	sourceRels, err := runtimeSuiteSourceRels(baseProjectDir)
	if err != nil {
		return "", err
	}
	for _, rel := range sourceRels {
		if err := copyFile(filepath.Join(baseProjectDir, "src", filepath.FromSlash(rel)), filepath.Join(tmpProj, "src", filepath.FromSlash(rel))); err != nil {
			return "", err
		}
	}
	if err := stageRotorOwnedSpecs(baseProjectDir, tmpProj); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmpProj, "src", "main.server.ts"), []byte(runtimeSuiteMainSource()), 0o644); err != nil {
		return "", err
	}
	return tmpProj, nil
}

// stageRotorOwnedSpecs copies testdata/conformance/rotor/*.spec.ts into the
// staged suite's src/tests. These specs exercise ROTOR EXTENSIONS that rbxtsc
// rejects outright (loop labels), so they deliberately live outside the shared
// conformance corpus: they have no byte-parity golden and must never be
// compared against rbxtsc output.
func stageRotorOwnedSpecs(baseProjectDir, tmpProj string) error {
	rotorSpecDir := filepath.Join(filepath.Dir(baseProjectDir), "rotor")
	entries, err := os.ReadDir(rotorSpecDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".spec.ts") {
			continue
		}
		if err := copyFile(
			filepath.Join(rotorSpecDir, entry.Name()),
			filepath.Join(tmpProj, "src", "tests", entry.Name()),
		); err != nil {
			return err
		}
	}
	return nil
}

func runtimeSuiteMainSource() string {
	return `/// <reference types="@rbxts/testez/globals" />

import { ServerScriptService } from "@rbxts/services";

const TestEZModule = game
	.GetService("ReplicatedStorage")
	.FindFirstChild("include")
	?.FindFirstChild("node_modules")
	?.FindFirstChild("@rbxts")
	?.FindFirstChild("testez")
	?.FindFirstChild("src") as ModuleScript | undefined;
assert(TestEZModule, "Unable to find @rbxts/testez!");
const TestEZ = require(TestEZModule) as typeof import("@rbxts/testez");

const results = TestEZ.TestBootstrap.run([ServerScriptService.tests]);
if (results.errors.size() > 0 || results.failureCount > 0) {
	error("Tests failed!");
}
`
}

func stageRuntimeTypeRoots(baseProjectDir, tmpProj string) error {
	src := filepath.Join(baseProjectDir, "node_modules", "@rbxts")
	dst := filepath.Join(tmpProj, "node_modules", "@rbxts")
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return patchRuntimeRoactCompat(filepath.Join(dst, "roact", "src", "init.lua"))
}

func patchRuntimeRoactCompat(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	patched, err := withRuntimeRoactCompat(string(data))
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(patched), 0o644)
}

func withRuntimeRoactCompat(text string) (string, error) {
	const marker = "return Roact"
	idx := strings.LastIndex(text, marker)
	if idx < 0 {
		return "", fmt.Errorf("roact init missing %q", marker)
	}
	if strings.Contains(text, "Roact.jsx = function(component, props, ...)") {
		return text, nil
	}
	return text[:idx] + runtimeRoactCompatLua() + text[idx:], nil
}

func runtimeRoactCompatLua() string {
	return `

local Type = require(script.Type)

local intrinsicComponentNames = {
	billboardgui = "BillboardGui",
	camera = "Camera",
	canvasgroup = "CanvasGroup",
	frame = "Frame",
	imagebutton = "ImageButton",
	imagelabel = "ImageLabel",
	screengui = "ScreenGui",
	scrollingframe = "ScrollingFrame",
	surfacegui = "SurfaceGui",
	textbox = "TextBox",
	textbutton = "TextButton",
	textlabel = "TextLabel",
	uiaspectratioconstraint = "UIAspectRatioConstraint",
	uicorner = "UICorner",
	uigradient = "UIGradient",
	uigridlayout = "UIGridLayout",
	uilistlayout = "UIListLayout",
	uipadding = "UIPadding",
	uipagelayout = "UIPageLayout",
	uiscale = "UIScale",
	uisizeconstraint = "UISizeConstraint",
	uistroke = "UIStroke",
	uitablelayout = "UITableLayout",
	uitextsizeconstraint = "UITextSizeConstraint",
	viewportframe = "ViewportFrame",
}

local Fragment = {}

local function cloneProps(props)
	if props == nil then
		return {}
	end

	local cloned = {}
	for key, value in pairs(props) do
		cloned[key] = value
	end
	return cloned
end

local function normalizeSpecialProps(props)
	local normalized = cloneProps(props)

	local ref = rawget(normalized, "Ref")
	if ref ~= nil then
		normalized.Ref = nil
		normalized[Roact.Ref] = ref
	end

	local events = rawget(normalized, "Event")
	if events ~= nil then
		normalized.Event = nil
		for eventName, listener in pairs(events) do
			normalized[Roact.Event[eventName]] = listener
		end
	end

	local changes = rawget(normalized, "Change")
	if changes ~= nil then
		normalized.Change = nil
		for propertyName, listener in pairs(changes) do
			normalized[Roact.Change[propertyName]] = listener
		end
	end

	local key = rawget(normalized, "Key")
	if key ~= nil then
		normalized.Key = nil
	end

	return normalized, key
end

local function getChildKey(child)
	if typeof(child) ~= "table" then
		return nil
	end
	return rawget(child, "_rotorKey")
end

local function normalizeChildren(...)
	local count = select("#", ...)
	if count == 0 then
		return nil
	end

	if count == 1 then
		local child = select(1, ...)
		if child == nil or typeof(child) == "boolean" then
			return nil
		end
		if typeof(child) == "table" and Type.of(child) ~= Type.Element then
			return child
		end
		local key = getChildKey(child)
		if key ~= nil then
			return { [key] = child }
		end
		return child
	end

	local children = {}
	local index = 1
	for i = 1, count do
		local child = select(i, ...)
		if child ~= nil and typeof(child) ~= "boolean" then
			local key = getChildKey(child)
			if key ~= nil then
				children[key] = child
			else
				children[index] = child
				index += 1
			end
		end
	end

	if next(children) == nil then
		return nil
	end
	return children
end

rawset(Roact, "Fragment", Fragment)

rawset(Roact, "jsx", function(component, props, ...)
	local normalizedProps, key = normalizeSpecialProps(props)
	local children = normalizeChildren(...)

	if component == Fragment then
		local fragment = Roact.createFragment(children)
		if key ~= nil then
			fragment._rotorKey = key
		end
		return fragment
	end

	if typeof(component) == "string" then
		component = intrinsicComponentNames[component] or (string.upper(string.sub(component, 1, 1)) .. string.sub(component, 2))
	end

	local element = Roact.createElement(component, normalizedProps, children)
	if key ~= nil then
		element._rotorKey = key
	end
	return element
end)

`
}

func runtimeRojoConfig() string {
	return `{
	"name": "conformance-runtime",
	"tree": {
		"$className": "DataModel",
		"ReplicatedStorage": {
			"$className": "ReplicatedStorage",
			"include": {
				"$path": "include",
				"node_modules": {
					"$className": "Folder",
					"@rbxts": {
						"$path": "node_modules/@rbxts"
					}
				}
			}
		},
		"ServerScriptService": {
			"$className": "ServerScriptService",
			"main": {
				"$path": "out/main.server.luau"
			},
			"tests": {
				"$path": "out/tests"
			},
			"helpers": {
				"$className": "Folder",
				"util": {
					"$path": "out/helpers/util"
				}
			}
		},
		"StarterGui": {
			"$className": "StarterGui",
			"isolated": {
				"$path": "out/helpers/rojo/isolated.luau"
			}
		}
	}
}`
}

func runBehavioralSuite(root string, tools RuntimeTools) error {
	baseProjectDir := filepath.Join(root, "testdata", "conformance", "project")
	if err := ensureConformanceProjectDeps(baseProjectDir); err != nil {
		return err
	}

	projDir, err := stageRuntimeSuiteProject(baseProjectDir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(projDir)

	buildCmd := exec.Command("go", "run", "./cmd/rotor", "build", projDir, "--type", "game", "--allowCommentDirectives")
	buildCmd.Dir = root
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("rotor build conformance project: %w", err)
	}

	placePath := filepath.Join(projDir, "conformance-tests.rbxlx")
	rojoCmd := exec.Command(tools.Rojo, "build", "--output", placePath, filepath.Join(projDir, "default.project.json"))
	rojoCmd.Dir = projDir
	rojoCmd.Stdout = os.Stdout
	rojoCmd.Stderr = os.Stderr
	if err := rojoCmd.Run(); err != nil {
		return fmt.Errorf("rojo build conformance project: %w", err)
	}

	luneCmd := exec.Command(tools.Lune, "run", filepath.Join(root, "reference", "roblox-ts", "tests", "runTestsWithLune.lua"), placePath)
	luneCmd.Dir = root
	luneCmd.Stdout = os.Stdout
	luneCmd.Stderr = os.Stderr
	if err := luneCmd.Run(); err != nil {
		return fmt.Errorf("lune behavioral suite: %w", err)
	}

	return nil
}
