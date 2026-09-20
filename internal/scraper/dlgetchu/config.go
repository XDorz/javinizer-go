package dlgetchu

import (
	"fmt"
	"github.com/javinizer/javinizer-go/internal/config"
	"github.com/javinizer/javinizer-go/internal/models"
	"regexp"
)

var validIDPrefix = regexp.MustCompile(`^[A-Za-z0-9_-]{0,32}$`)
var prefixLetter = regexp.MustCompile(`[A-Za-z]`)
var ambiguousIDPrefix = regexp.MustCompile(`(?i)^(?:getchu[-_]|item)[0-9]+$`)

// validateScraperSettings performs scraper-specific validation for dlgetchu.
func validateScraperSettings(ss *models.ScraperSettings) error {
	if err := config.ValidateHTTPBaseURL("dlgetchu.base_url", ss.BaseURL); err != nil {
		return err
	}
	if ss.IDPrefix != nil {
		prefix := *ss.IDPrefix
		if ambiguousIDPrefix.MatchString(prefix) {
			return fmt.Errorf("dlgetchu.id_prefix must not be a complete Getchu ID (for example getchu-123 or item12), which makes canonical inputs ambiguous")
		}
		if !validIDPrefix.MatchString(prefix) || (prefix != "" && !prefixLetter.MatchString(prefix)) {
			return fmt.Errorf("dlgetchu.id_prefix must be empty or up to 32 ASCII letters, digits, underscores or hyphens, including at least one letter")
		}
	}
	return nil
}
