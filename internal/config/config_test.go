package config

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, fixture string) *Config {
	t.Helper()
	cfg, err := Load(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("Load(%q): %v", fixture, err)
	}
	return cfg
}

func TestLoadValidFullConfig(t *testing.T) {
	cfg := load(t, "valid")

	if cfg.Assets == nil {
		t.Fatal("assets section missing")
	}
	if cfg.Assets.Mode != "macro" {
		t.Errorf("assets.mode = %q, want macro", cfg.Assets.Mode)
	}
	wantPaths := []string{"assets/**/*.png", "assets/**/*.ogg"}
	if len(cfg.Assets.Paths) != len(wantPaths) {
		t.Fatalf("assets.paths = %v, want %v", cfg.Assets.Paths, wantPaths)
	}
	for i, p := range wantPaths {
		if cfg.Assets.Paths[i] != p {
			t.Errorf("assets.paths[%d] = %q, want %q", i, cfg.Assets.Paths[i], p)
		}
	}
	if cfg.Assets.Output.Luau != "src/shared/assets.luau" {
		t.Errorf("assets.output.luau = %q", cfg.Assets.Output.Luau)
	}
	if cfg.Assets.Output.Types != "src/shared/assets.d.ts" {
		t.Errorf("assets.output.types = %q", cfg.Assets.Output.Types)
	}
	if cfg.Assets.Creator.Type != "group" || cfg.Assets.Creator.ID != 12345 {
		t.Errorf("assets.creator = %+v", cfg.Assets.Creator)
	}

	if cfg.Deploy == nil {
		t.Fatal("deploy section missing")
	}
	if len(cfg.Deploy.Environments) != 2 {
		t.Fatalf("environments = %v", cfg.Deploy.Environments)
	}

	dev := cfg.Deploy.Environments["dev"]
	if dev.UniverseID != 111 {
		t.Errorf("dev.universeId = %d", dev.UniverseID)
	}
	if dev.Payments != "free" {
		t.Errorf("dev.payments = %q", dev.Payments)
	}
	if p := dev.Places["start"]; p.File != "build/game.rbxl" || p.PlaceID != 222 {
		t.Errorf("dev.places.start = %+v", p)
	}
	if dev.Experience != nil {
		t.Errorf("dev.experience = %+v, want nil", dev.Experience)
	}

	prod := cfg.Deploy.Environments["prod"]
	if prod.UniverseID != 333 {
		t.Errorf("prod.universeId = %d", prod.UniverseID)
	}
	if len(prod.Places) != 2 {
		t.Errorf("prod.places = %+v", prod.Places)
	}
	if p := prod.Places["lobby"]; p.File != "build/lobby.rbxl" || p.PlaceID != 555 {
		t.Errorf("prod.places.lobby = %+v", p)
	}
	if prod.Experience == nil {
		t.Fatal("prod.experience missing")
	}
	if prod.Experience.Name != "My Game" ||
		prod.Experience.Description != "The best game" ||
		prod.Experience.Playability != "public" {
		t.Errorf("prod.experience = %+v", prod.Experience)
	}
	if prod.Payments != "paid" {
		t.Errorf("prod.payments = %q", prod.Payments)
	}
	b := prod.Badges["winner"]
	if b.Name != "Winner!" || b.Description != "You won" || b.Icon != "assets/badge.png" {
		t.Errorf("prod.badges.winner = %+v", b)
	}
	if p := prod.Places["start"]; p.Name != "Start Place" || p.MaxPlayers != 30 || p.VersionType != "saved" {
		t.Errorf("prod.places.start metadata = %+v", p)
	}
	if ps := prod.Experience.PrivateServers; ps == nil || ps.Price == nil || *ps.Price != 100 {
		t.Errorf("prod.experience.privateServers = %+v", ps)
	}
	g := prod.GamePasses["vip"]
	if g.Name != "VIP" || g.Price == nil || *g.Price != 250 || g.Icon != "assets/vip.png" {
		t.Errorf("prod.gamepasses.vip = %+v", g)
	}
	if prod.Icon != "assets/icon.png" {
		t.Errorf("prod.icon = %q", prod.Icon)
	}
	if len(prod.Thumbnails) != 2 || prod.Thumbnails[0] != "assets/thumb1.png" {
		t.Errorf("prod.thumbnails = %v", prod.Thumbnails)
	}
	if dp := prod.Products["coins"]; dp.Name != "100 Coins" || dp.Price != 25 {
		t.Errorf("prod.products.coins = %+v", dp)
	}
	if sl := prod.SocialLinks["discord"]; sl.Title != "Join us" || sl.URL != "https://discord.gg/x" || sl.Type != "discord" {
		t.Errorf("prod.socials.discord = %+v", sl)
	}

	if len(cfg.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", cfg.Warnings)
	}
	if errs := cfg.Validate(); len(errs) != 0 {
		t.Errorf("Validate() = %v, want clean", errs)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if cfg != nil {
		t.Fatalf("cfg = %+v, want nil", cfg)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLoadUnknownKeyWarns(t *testing.T) {
	cfg := load(t, "unknownkey")
	if len(cfg.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", cfg.Warnings)
	}
	if !strings.Contains(cfg.Warnings[0], `"analytics"`) {
		t.Fatalf("warning = %q, want mention of the unknown key", cfg.Warnings[0])
	}
	if cfg.Assets == nil || cfg.Assets.Creator.ID != 1 {
		t.Fatalf("known sections must still load: %+v", cfg.Assets)
	}
}

func TestLoadBadModeValidates(t *testing.T) {
	cfg := load(t, "badmode")
	errs := cfg.Validate()
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "assets.mode") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Validate() = %v, want an assets.mode error", errs)
	}
}

func TestLoadSyntaxError(t *testing.T) {
	// A malformed rotor.toml is a hard load error (not ErrNotFound).
	dir := t.TempDir()
	writeConfigFile(t, dir, "rotor.toml", "this is = not [valid toml")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected an error for malformed toml")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed toml should not be ErrNotFound: %v", err)
	}
	if !strings.Contains(err.Error(), "rotor.toml") {
		t.Fatalf("err = %v, want mention of rotor.toml", err)
	}
}

func TestValidate(t *testing.T) {
	t.Run("bad assets mode", func(t *testing.T) {
		cfg := &Config{Assets: &AssetsConfig{Mode: "bundle", Creator: Creator{Type: "user", ID: 1}}}
		errs := cfg.Validate()
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "assets.mode") {
			t.Fatalf("Validate() = %v, want one assets.mode error", errs)
		}
	})

	t.Run("empty mode is module default", func(t *testing.T) {
		cfg := &Config{Assets: &AssetsConfig{Creator: Creator{Type: "user", ID: 1}}}
		if errs := cfg.Validate(); len(errs) != 0 {
			t.Fatalf("Validate() = %v, want clean", errs)
		}
	})

	t.Run("module and macro modes ok", func(t *testing.T) {
		for _, m := range []string{"module", "macro"} {
			cfg := &Config{Assets: &AssetsConfig{Mode: m, Creator: Creator{Type: "user", ID: 1}}}
			if errs := cfg.Validate(); len(errs) != 0 {
				t.Fatalf("Validate(mode=%q) = %v, want clean", m, errs)
			}
		}
	})

	t.Run("bad creator type", func(t *testing.T) {
		cfg := &Config{Assets: &AssetsConfig{Creator: Creator{Type: "person", ID: 1}}}
		errs := cfg.Validate()
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "creator.type") {
			t.Fatalf("Validate() = %v, want one creator.type error", errs)
		}
	})

	t.Run("place missing file and placeId", func(t *testing.T) {
		cfg := &Config{Deploy: &DeployConfig{Environments: map[string]Environment{
			"dev": {UniverseID: 1, Places: map[string]PlaceDeploy{"start": {}}},
		}}}
		errs := cfg.Validate()
		if len(errs) != 2 {
			t.Fatalf("Validate() = %v, want file + placeId errors", errs)
		}
	})

	t.Run("bad playability", func(t *testing.T) {
		cfg := &Config{Deploy: &DeployConfig{Environments: map[string]Environment{
			"prod": {UniverseID: 1, Experience: &ExperienceConfig{Playability: "everyone"}},
		}}}
		errs := cfg.Validate()
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "playability") {
			t.Fatalf("Validate() = %v, want one playability error", errs)
		}
	})

	t.Run("empty config is valid", func(t *testing.T) {
		if errs := (&Config{}).Validate(); len(errs) != 0 {
			t.Fatalf("Validate() = %v, want clean", errs)
		}
	})

	env := func(e Environment) *Config {
		return &Config{Deploy: &DeployConfig{Environments: map[string]Environment{"prod": e}}}
	}
	neg := int64(-5)
	free := int64(0)
	table := []struct {
		name    string
		cfg     *Config
		wantErr string // substring of the single expected error; "" = clean
	}{
		{"bad versionType",
			env(Environment{Places: map[string]PlaceDeploy{"s": {File: "f", PlaceID: 1, VersionType: "live"}}}),
			"versionType"},
		{"saved versionType ok",
			env(Environment{Places: map[string]PlaceDeploy{"s": {File: "f", PlaceID: 1, VersionType: "saved"}}}),
			""},
		{"negative maxPlayers",
			env(Environment{Places: map[string]PlaceDeploy{"s": {File: "f", PlaceID: 1, MaxPlayers: -1}}}),
			"maxPlayers"},
		{"negative game pass price",
			env(Environment{GamePasses: map[string]GamePass{"vip": {Name: "V", Price: &neg}}}),
			"gamepasses.vip.price"},
		{"nil game pass price ok (off sale)",
			env(Environment{GamePasses: map[string]GamePass{"vip": {Name: "V"}}}),
			""},
		{"negative product price",
			env(Environment{Products: map[string]Product{"coins": {Name: "C", Price: -1}}}),
			"products.coins.price"},
		{"negative private server price",
			env(Environment{Experience: &ExperienceConfig{PrivateServers: &PrivateServers{Price: &neg}}}),
			"privateServers.price"},
		{"zero private server price ok",
			env(Environment{Experience: &ExperienceConfig{PrivateServers: &PrivateServers{Price: &free}}}),
			""},
		{"bad social link type",
			env(Environment{SocialLinks: map[string]SocialLink{"x": {Type: "myspace", URL: "https://x"}}}),
			"socials.x.type"},
		{"social link missing url",
			env(Environment{SocialLinks: map[string]SocialLink{"x": {Type: "discord"}}}),
			"url is required"},
		{"valid social link",
			env(Environment{SocialLinks: map[string]SocialLink{"x": {Title: "t", Type: "github", URL: "https://github.com/x"}}}),
			""},
		{"eleven thumbnails",
			env(Environment{Thumbnails: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}}),
			"at most 10"},
		{"ten thumbnails ok",
			env(Environment{Thumbnails: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}}),
			""},
	}
	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.cfg.Validate()
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("Validate() = %v, want clean", errs)
				}
				return
			}
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want one error containing %q", errs, tc.wantErr)
			}
		})
	}
}

func TestSchemaIsValidJSON(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal([]byte(Schema), &root); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	props, ok := root["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no top-level properties object")
	}
	for _, want := range []string{"assets", "deploy"} {
		if _, ok := props[want]; !ok {
			t.Errorf("schema properties missing %q", want)
		}
	}
	// $id / title are set.
	if root["$id"] == nil || root["title"] == nil {
		t.Errorf("schema missing $id/title: $id=%v title=%v", root["$id"], root["title"])
	}
	// At least one enum exists (assets.mode), proving enums are encoded.
	assets, _ := props["assets"].(map[string]any)
	aprops, _ := assets["properties"].(map[string]any)
	mode, _ := aprops["mode"].(map[string]any)
	enum, _ := mode["enum"].([]any)
	if len(enum) != 2 {
		t.Errorf("assets.mode enum = %v, want two values (module, macro)", mode["enum"])
	}
}

// assets.base must be a relative directory inside the project.
func TestValidateAssetsBase(t *testing.T) {
	valid := []string{"", "assets", "assets/images", "./assets/images"}
	for _, base := range valid {
		c := &Config{Assets: &AssetsConfig{Mode: "macro", Base: base, Creator: Creator{Type: "user", ID: 1}}}
		if errs := c.Validate(); len(errs) != 0 {
			t.Errorf("base %q: unexpected errors %v", base, errs)
		}
	}
	invalid := []string{"/abs/path", "../outside", "assets/../../outside"}
	for _, base := range invalid {
		c := &Config{Assets: &AssetsConfig{Mode: "macro", Base: base, Creator: Creator{Type: "user", ID: 1}}}
		if errs := c.Validate(); len(errs) == 0 {
			t.Errorf("base %q: expected a validation error", base)
		}
	}
}
