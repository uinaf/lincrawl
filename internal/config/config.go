// Package config loads lincrawl runtime configuration from environment
// variables, a git-ignored .env.local, and optional XDG-resolved paths.
//
// LINEAR_API_KEY is the only required key, and only for live calls. The
// offline fixture path runs without any credentials.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	AppID       = "lincrawl"
	DisplayName = "lincrawl"

	EnvHome            = "LINCRAWL_HOME"
	EnvLinearAPIKey    = "LINEAR_API_KEY"
	EnvLinearBaseURL   = "LINCRAWL_LINEAR_BASE_URL"
	EnvAgeRecipient    = "LINCRAWL_AGE_RECIPIENT"
	EnvAgeIdentity     = "LINCRAWL_AGE_IDENTITY"
	DefaultLinearAPI   = "https://api.linear.app/graphql"
	DefaultDotEnvLocal = ".env.local"
)

// Runtime is the resolved set of paths and credential-presence flags. Actual
// secret values stay in the process environment; only presence is reported.
type Runtime struct {
	Home               string
	DatabasePath       string
	ConfigDir          string
	DotEnvPath         string
	LinearAPIBase      string
	LinearAPIKeySet    bool
	AgeRecipientSet    bool
	AgeIdentitySet     bool
	LoadedDotEnv       bool
	LoadedDotEnvSource string
}

// LoadRuntime reads .env.local if present (without overriding the live shell)
// then derives the runtime paths. It does not create directories; callers
// invoke EnsureDirs separately.
func LoadRuntime() (Runtime, error) {
	rt := Runtime{LinearAPIBase: DefaultLinearAPI}

	if loaded, source, err := LoadDotEnv(DefaultDotEnvLocal); err != nil {
		return Runtime{}, err
	} else {
		rt.LoadedDotEnv = loaded
		rt.LoadedDotEnvSource = source
	}

	home := strings.TrimSpace(os.Getenv(EnvHome))
	if home == "" {
		xdg, err := defaultDataDir()
		if err != nil {
			return Runtime{}, err
		}
		home = xdg
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return Runtime{}, err
	}
	home = absHome
	rt.Home = home
	rt.DatabasePath = filepath.Join(home, "lincrawl.db")
	rt.ConfigDir = filepath.Join(home, "config")
	rt.DotEnvPath = DefaultDotEnvLocal

	if base := strings.TrimSpace(os.Getenv(EnvLinearBaseURL)); base != "" {
		rt.LinearAPIBase = base
	}
	rt.LinearAPIKeySet = strings.TrimSpace(os.Getenv(EnvLinearAPIKey)) != ""
	rt.AgeRecipientSet = strings.TrimSpace(os.Getenv(EnvAgeRecipient)) != ""
	rt.AgeIdentitySet = strings.TrimSpace(os.Getenv(EnvAgeIdentity)) != ""

	return rt, nil
}

// EnsureDirs creates the runtime home and config directories.
func EnsureDirs(rt Runtime) error {
	if err := os.MkdirAll(rt.Home, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(rt.ConfigDir, 0o755)
}

// LinearAPIKey returns the token from the process environment. Callers must
// not log this value.
func LinearAPIKey() string { return strings.TrimSpace(os.Getenv(EnvLinearAPIKey)) }

// LoadDotEnv parses a KEY=VALUE file and exports variables that are not
// already set in the shell. Returns (loaded, absolute path, error).
func LoadDotEnv(path string) (bool, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, "", err
	}
	f, err := os.Open(filepath.Clean(abs))
	if err != nil {
		if os.IsNotExist(err) {
			return false, abs, nil
		}
		return false, abs, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		key, val, ok := strings.Cut(raw, "=")
		if !ok {
			return false, abs, fmt.Errorf("parse %s:%d: expected KEY=VALUE", path, lineNo)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, present := os.LookupEnv(key); present {
			continue
		}
		os.Setenv(key, unquote(strings.TrimSpace(val)))
	}
	if err := scanner.Err(); err != nil {
		return false, abs, err
	}
	return true, abs, nil
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

// Redact returns "set" or "unset" for boolean presence flags so callers can
// emit JSON status without exposing values.
func Redact(set bool) string {
	if set {
		return "set"
	}
	return "unset"
}

func defaultDataDir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, AppID), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", AppID), nil
}
