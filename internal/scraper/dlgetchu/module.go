package dlgetchu

import (
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/javinizer/javinizer-go/internal/scraperutil"
)

// Register registers the DLGetchu scraper with the given registrar.
func Register(reg scraperutil.ScraperRegistrar) {
	idPrefix := defaultIDPrefix
	reg.Register(scraperutil.ScraperRegistration{
		Name:        "dlgetchu",
		Description: "DLGetchu",
		Options: []models.ScraperOption{
			{
				Key: "id_prefix", Label: "Output ID prefix", Type: "string",
				Description: "Prefix added to verified DLGetchu IDs; leave empty for numeric IDs. Existing cached metadata is unchanged.",
				Default:     defaultIDPrefix,
			},
			{
				Key:         "rate_limit",
				Label:       "Rate Limit",
				Description: "Delay between requests in milliseconds to avoid rate limiting",
				Type:        "number",
				Min:         scraperutil.IntPtr(0),
				Max:         scraperutil.IntPtr(5000),
				Unit:        "ms",
			},
			{
				Key:         "base_url",
				Label:       "Base URL",
				Description: "DLGetchu base URL",
				Type:        "string",
			},
		},
		Defaults: models.ScraperSettings{
			Enabled:   false,
			RateLimit: 1000,
			BaseURL:   defaultBaseURL,
			IDPrefix:  &idPrefix,
		},
		Priority: 40,
		Constructor: func(deps scraperutil.ScraperDeps) (models.Scraper, error) {
			return newScraper(&deps.Settings, deps.GlobalProxy, deps.FlareSolverr), nil
		},
		ValidateFn: validateScraperSettings,
	})
}
