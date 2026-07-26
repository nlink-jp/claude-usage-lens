// Package config loads claude-usage-lens's optional TOML configuration.
//
// Every setting is optional and a missing file is not an error — an
// unconfigured install runs entirely on the OS-inferred defaults. Precedence,
// highest first:
//
//	CLI flags  >  config file  >  built-in / OS-inferred defaults
//
// The default location is <OS config dir>/claude-usage-lens/config.toml (see
// core/platform). Decoding is strict: an unrecognised key is a hard error rather
// than a silently ignored one, because a typo'd setting that appears to work is
// the worst outcome for a file whose whole job is to override behaviour.
//
// Merging is expressed as pure functions over explicit inputs (Roots, Table),
// so the resolution rules are unit-testable without touching the filesystem.
package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/nlink-jp/claude-usage-lens/core/platform"
	"github.com/nlink-jp/claude-usage-lens/core/pricing"
)

// FileName is the config file's name inside the OS config dir.
const FileName = "config.toml"

// Config is the root of config.toml. A zero Config is valid and changes nothing.
type Config struct {
	Sources Sources `toml:"sources"`
	Pricing Pricing `toml:"pricing"`
}

// Sources overrides the auto-detected source roots. This is the safety valve for
// environments where the OS inference is wrong — notably Windows, where the
// paths are unverified on real hardware. An empty field keeps the default.
type Sources struct {
	CodeRoot   string `toml:"code_root"`
	CoworkRoot string `toml:"cowork_root"`
}

// Pricing overrides or extends the built-in rate table, keyed by model id
// (`[pricing.models."claude-opus-5"]`).
type Pricing struct {
	Models map[string]RateOverride `toml:"models"`
}

// RateOverride is a *partial* rate entry: every field is optional, and an
// omitted field inherits from the built-in table entry (or, for a model the
// table does not know, from the standard multipliers).
//
// The fields are pointers precisely so "omitted" and "explicitly 0" are
// distinguishable. With plain floats, overriding just input_per_mtok would
// silently zero every cache multiplier — the same silent-undercount failure
// mode this table exists to prevent.
type RateOverride struct {
	InputPerMTok           *float64 `toml:"input_per_mtok"`
	OutputPerMTok          *float64 `toml:"output_per_mtok"`
	CacheReadMultiplier    *float64 `toml:"cache_read_multiplier"`
	CacheWrite1hMultiplier *float64 `toml:"cache_write_1h_multiplier"`
	CacheWrite5mMultiplier *float64 `toml:"cache_write_5m_multiplier"`
	WebSearchPerReq        *float64 `toml:"web_search_per_req"`
	WebFetchPerReq         *float64 `toml:"web_fetch_per_req"`
}

// DefaultPath returns <OS config dir>/config.toml — where `doctor` says to put it.
func DefaultPath() (string, error) {
	dir, err := platform.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the config at path, or at DefaultPath when path is empty. It
// returns the resolved path and whether a file was actually found, so callers
// (notably `doctor`) can tell "using defaults" from "loaded overrides".
//
// A missing file yields a zero Config and found=false, not an error. A file that
// exists but cannot be parsed — or that contains an unknown key — IS an error:
// silently ignoring it would leave the user believing a setting took effect.
func Load(path string) (cfg *Config, resolvedPath string, found bool, err error) {
	cfg = &Config{}
	if path == "" {
		path, err = DefaultPath()
		if err != nil {
			return cfg, "", false, err
		}
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return cfg, path, false, nil
		}
		return cfg, path, false, statErr
	}

	md, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return cfg, path, true, fmt.Errorf("parse config %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return cfg, path, true, fmt.Errorf(
			"unknown key(s) in %s: %s — see config.example.toml for the accepted schema",
			path, strings.Join(keys, ", "))
	}
	if err := cfg.Validate(); err != nil {
		return cfg, path, true, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, path, true, nil
}

// Validate rejects values that would silently corrupt cost figures. A negative
// rate is always a mistake, and a blank model key cannot match any record.
func (c *Config) Validate() error {
	for name, ov := range c.Pricing.Models {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("[pricing.models] has an empty model id")
		}
		for _, f := range []struct {
			key string
			val *float64
		}{
			{"input_per_mtok", ov.InputPerMTok},
			{"output_per_mtok", ov.OutputPerMTok},
			{"cache_read_multiplier", ov.CacheReadMultiplier},
			{"cache_write_1h_multiplier", ov.CacheWrite1hMultiplier},
			{"cache_write_5m_multiplier", ov.CacheWrite5mMultiplier},
			{"web_search_per_req", ov.WebSearchPerReq},
			{"web_fetch_per_req", ov.WebFetchPerReq},
		} {
			if f.val != nil && *f.val < 0 {
				return fmt.Errorf("[pricing.models.%q] %s is negative (%v)", name, f.key, *f.val)
			}
		}
	}
	return nil
}

// Roots applies the [sources] overrides on top of the OS-inferred defaults.
// An unset field keeps the default; the result is what every source scan uses.
func (c *Config) Roots(def platform.Roots) platform.Roots {
	if c == nil {
		return def
	}
	if v := strings.TrimSpace(c.Sources.CodeRoot); v != "" {
		def.CodeRoot = v
	}
	if v := strings.TrimSpace(c.Sources.CoworkRoot); v != "" {
		def.CoworkRoot = v
	}
	return def
}

// PricingTable applies the [pricing.models] overrides on top of base, returning
// a new table (base is not mutated). A model absent from base is added, starting
// from the standard multipliers so a two-line override is enough to price a
// model this build has never heard of.
func (c *Config) PricingTable(base pricing.Table) pricing.Table {
	out := make(pricing.Table, len(base))
	maps.Copy(out, base)
	if c == nil {
		return out
	}
	for name, ov := range c.Pricing.Models {
		r, known := out[name]
		if !known {
			r = pricing.StandardRates(0, 0)
		}
		setIf(&r.InputPerMTok, ov.InputPerMTok)
		setIf(&r.OutputPerMTok, ov.OutputPerMTok)
		setIf(&r.CacheReadMultiplier, ov.CacheReadMultiplier)
		setIf(&r.CacheWrite1hMultiplier, ov.CacheWrite1hMultiplier)
		setIf(&r.CacheWrite5mMultiplier, ov.CacheWrite5mMultiplier)
		setIf(&r.WebSearchPerReq, ov.WebSearchPerReq)
		setIf(&r.WebFetchPerReq, ov.WebFetchPerReq)
		out[name] = r
	}
	return out
}

// OverriddenModels lists the model ids the config touches, sorted — used to tell
// the user which prices are theirs rather than the built-in ones.
func (c *Config) OverriddenModels() []string {
	if c == nil || len(c.Pricing.Models) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Pricing.Models))
	for name := range c.Pricing.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func setIf(dst *float64, src *float64) {
	if src != nil {
		*dst = *src
	}
}
