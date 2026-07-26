package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nlink-jp/claude-usage-lens/core/platform"
	"github.com/nlink-jp/claude-usage-lens/core/pricing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// An absent file must behave exactly like an empty one: every setting optional.
func TestLoad_MissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	cfg, resolved, found, err := Load(path)
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if found {
		t.Error("found = true for a file that does not exist")
	}
	if resolved != path {
		t.Errorf("resolved path = %q, want %q", resolved, path)
	}
	if got := cfg.Roots(platform.Roots{CodeRoot: "/d/code"}); got.CodeRoot != "/d/code" {
		t.Errorf("empty config changed the defaults: %+v", got)
	}
}

func TestLoad_SourcesOverride(t *testing.T) {
	path := write(t, `
[sources]
code_root = "/custom/code"
`)
	cfg, _, found, err := Load(path)
	if err != nil || !found {
		t.Fatalf("Load: found=%v err=%v", found, err)
	}
	got := cfg.Roots(platform.Roots{CodeRoot: "/default/code", CoworkRoot: "/default/cowork"})
	if got.CodeRoot != "/custom/code" {
		t.Errorf("code_root not applied: %q", got.CodeRoot)
	}
	// An unset field must keep the OS-inferred default, not blank it.
	if got.CoworkRoot != "/default/cowork" {
		t.Errorf("unset cowork_root should keep the default, got %q", got.CoworkRoot)
	}
}

// The point of pointer fields: a partial override must not zero the rest.
func TestPricingTable_PartialOverrideKeepsBuiltInFields(t *testing.T) {
	path := write(t, `
[pricing.models."claude-opus-5"]
input_per_mtok = 4.0
`)
	cfg, _, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	tbl := cfg.PricingTable(pricing.Default())
	r, ok := tbl.Lookup("claude-opus-5")
	if !ok {
		t.Fatal("model disappeared from the table")
	}
	if r.InputPerMTok != 4.0 {
		t.Errorf("input = %v, want 4.0", r.InputPerMTok)
	}
	if r.OutputPerMTok != 25 {
		t.Errorf("output = %v, want the built-in 25 (untouched)", r.OutputPerMTok)
	}
	if r.CacheReadMultiplier != 0.10 || r.CacheWrite5mMultiplier != 1.25 || r.CacheWrite1hMultiplier != 2.00 {
		t.Errorf("multipliers were clobbered by a partial override: %+v", r)
	}
	if r.WebSearchPerReq != 0.01 {
		t.Errorf("web_search_per_req = %v, want the built-in 0.01", r.WebSearchPerReq)
	}
}

// Explicit 0 must be honoured — that is what pointers buy over plain floats.
func TestPricingTable_ExplicitZeroIsApplied(t *testing.T) {
	path := write(t, `
[pricing.models."claude-opus-5"]
web_search_per_req = 0.0
`)
	cfg, _, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := cfg.PricingTable(pricing.Default()).Lookup("claude-opus-5")
	if r.WebSearchPerReq != 0 {
		t.Errorf("explicit 0 not applied: %v", r.WebSearchPerReq)
	}
}

// A model this build has never heard of can be priced from config alone, and
// gets sane cache multipliers without spelling all seven fields out.
func TestPricingTable_AddsUnknownModelWithStandardMultipliers(t *testing.T) {
	path := write(t, `
[pricing.models."claude-future-9"]
input_per_mtok  = 7.0
output_per_mtok = 35.0
`)
	cfg, _, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	base := pricing.Default()
	tbl := cfg.PricingTable(base)

	r, ok := tbl.Lookup("claude-future-9")
	if !ok {
		t.Fatal("config-defined model not added to the table")
	}
	if r.InputPerMTok != 7 || r.OutputPerMTok != 35 {
		t.Errorf("base prices wrong: %+v", r)
	}
	if r.CacheReadMultiplier != 0.10 || r.CacheWrite1hMultiplier != 2.00 {
		t.Errorf("standard multipliers not applied: %+v", r)
	}
	// The built-in table must not be mutated.
	if _, leaked := base["claude-future-9"]; leaked {
		t.Error("PricingTable mutated the base table")
	}
}

// A typo'd key that silently does nothing is the failure this file exists to
// prevent, so it must be loud.
func TestLoad_UnknownKeyIsAnError(t *testing.T) {
	path := write(t, `
[sources]
code_roots = "/typo"
`)
	_, _, _, err := Load(path)
	if err == nil {
		t.Fatal("unknown key should be rejected")
	}
	if !strings.Contains(err.Error(), "code_roots") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
}

func TestLoad_MalformedTOMLIsAnError(t *testing.T) {
	if _, _, _, err := Load(write(t, "[sources\n")); err == nil {
		t.Fatal("malformed TOML should be rejected")
	}
}

func TestLoad_NegativeRateIsAnError(t *testing.T) {
	path := write(t, `
[pricing.models."claude-opus-5"]
input_per_mtok = -1.0
`)
	_, _, _, err := Load(path)
	if err == nil {
		t.Fatal("negative rate should be rejected")
	}
	if !strings.Contains(err.Error(), "input_per_mtok") {
		t.Errorf("error should name the offending field, got: %v", err)
	}
}

func TestOverriddenModels(t *testing.T) {
	path := write(t, `
[pricing.models."claude-sonnet-5"]
input_per_mtok = 2.0
[pricing.models."claude-opus-5"]
input_per_mtok = 5.0
`)
	cfg, _, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.OverriddenModels()
	want := []string{"claude-opus-5", "claude-sonnet-5"} // sorted
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("OverriddenModels() = %v, want %v", got, want)
	}
	if (*Config)(nil).OverriddenModels() != nil {
		t.Error("nil config should report no overrides")
	}
}

// Fast-mode rates are overridable too, and setting them on a model with no
// built-in fast tier is what makes that model fast-capable.
func TestPricingTable_FastRateOverride(t *testing.T) {
	path := write(t, `
[pricing.models."claude-sonnet-5"]
fast_input_per_mtok  = 6.0
fast_output_per_mtok = 30.0
`)
	cfg, _, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := cfg.PricingTable(pricing.Default()).Lookup("claude-sonnet-5")
	if !r.HasFast() {
		t.Fatal("config should be able to add a fast tier")
	}
	if in, out := r.Base(pricing.SpeedFast); in != 6 || out != 30 {
		t.Errorf("Base(fast) = %v/%v, want 6/30", in, out)
	}
	// The standard pair is untouched.
	if in, _ := r.Base(pricing.SpeedStandard); in != 3 {
		t.Errorf("Base(standard) = %v, want the built-in 3", in)
	}
}

func TestLoad_NegativeFastRateIsAnError(t *testing.T) {
	path := write(t, `
[pricing.models."claude-opus-5"]
fast_input_per_mtok = -1.0
`)
	if _, _, _, err := Load(path); err == nil {
		t.Fatal("negative fast rate should be rejected")
	}
}
