package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ─── Types ────────────────────────────────────────────────────────────────────

type Cryptocurrency struct {
	Symbol    string   `yaml:"symbol"`
	DataTypes []string `yaml:"datatypes"`
	Intervals []string `yaml:"intervals"`
}

type Download struct {
	MaxConcurrentDownloads   int  `yaml:"max_concurrent_downloads"`
	OverwriteDownloadedFiles bool `yaml:"overwrite_downloaded_files"`
	VerifyContentIntegrity   bool `yaml:"verify_content_integrity"`
}

type Extraction struct {
	Enable                  bool `yaml:"enable"`
	RemoveAfterExtraction   bool `yaml:"remove_after_extraction"`
	OverwriteExtractedFiles bool `yaml:"overwrite_extracted_files"`
}

type Fetcher struct {
	SkipChecksumVerification bool             `yaml:"skip_checksum_verification"`
	Download                 Download         `yaml:"download"`
	Extraction               Extraction       `yaml:"extraction"`
	Cryptocurrencies         []Cryptocurrency `yaml:"cryptocurrencies"`
}

// Config holds everything df needs: where to store data and how the
// fetcher should behave. Unlike aegis's AegisConfig, there is no daemon or
// session-orchestration state here - df is a standalone data fetcher.
type Config struct {
	DataPath string  `yaml:"data_path"`
	Fetcher  Fetcher `yaml:"fetcher"`
}

// ─── Defaults ─────────────────────────────────────────────────────────────────

func Default() *Config {
	return &Config{
		DataPath: "~/df/data",
		Fetcher: Fetcher{
			SkipChecksumVerification: false,
			Extraction: Extraction{
				Enable:                  true,
				RemoveAfterExtraction:   false,
				OverwriteExtractedFiles: false,
			},
			Download: Download{
				MaxConcurrentDownloads:   5,
				OverwriteDownloadedFiles: false,
				VerifyContentIntegrity:   false,
			},
			Cryptocurrencies: []Cryptocurrency{},
		},
	}
}

// ─── Loader ───────────────────────────────────────────────────────────────────

// Load loads and validates df.yaml.
func Load() (*Config, error) {
	path, err := findConfig("df.yaml")
	if err != nil {
		return nil, err
	}

	cfg, err := parseConfig(path)
	if err != nil {
		return nil, err
	}

	// Environment variable takes precedence over data_path in yaml
	if v := os.Getenv("DF_DATA_PATH"); v != "" {
		cfg.DataPath = v
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	cfg.DataPath = expandHome(cfg.DataPath)
	return cfg, nil
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// findConfig looks for filename in ~/.config/df/ first, then config/ locally.
func findConfig(filename string) (string, error) {
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".config", "df", filename)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	p := filepath.Join("config", filename)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("%s not found in ~/.config/df/ or config/", filename)
}

func parseConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	var errs []string

	if c.DataPath == "" {
		errs = append(errs, "data_path is required (or set DF_DATA_PATH)")
	}

	for i, crypto := range c.Fetcher.Cryptocurrencies {
		if crypto.Symbol == "" {
			errs = append(errs, fmt.Sprintf("fetcher.cryptocurrencies[%d].symbol is required", i))
		}
		if len(crypto.DataTypes) == 0 {
			errs = append(errs, fmt.Sprintf("fetcher.cryptocurrencies[%d].datatypes is required", i))
		}
		if len(crypto.Intervals) == 0 {
			errs = append(errs, fmt.Sprintf("fetcher.cryptocurrencies[%d].intervals is required", i))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
