package matcher

import "github.com/javinizer/javinizer-go/internal/config"

// Config holds the subset of application configuration needed by the Matcher.
type Config struct {
	RegexEnabled     bool
	RegexPattern     string
	DLGetchuIDPrefix string
}

// ConfigFromAppConfig extracts Matcher-relevant fields from the application config.
//
// Config-bridge reads: cfg.Matching.RegexEnabled, cfg.Matching.RegexPattern
func ConfigFromAppConfig(cfg *config.Config) *Config {
	if cfg == nil {
		return nil
	}
	return &Config{
		RegexEnabled:     cfg.Matching.RegexEnabled,
		RegexPattern:     cfg.Matching.RegexPattern,
		DLGetchuIDPrefix: DLGetchuPrefixFromAppConfig(cfg),
	}
}

// DLGetchuPrefixFromAppConfig preserves the scraper's current output spelling
// when matching files that were organized with a custom prefix.
func DLGetchuPrefixFromAppConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	settings := cfg.Scrapers.ResolvedSettings("dlgetchu")
	if settings.IDPrefix == nil {
		return ""
	}
	return *settings.IDPrefix
}
