package dlgetchu

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-resty/resty/v2"
	"github.com/javinizer/javinizer-go/internal/challengedetect"
	"github.com/javinizer/javinizer-go/internal/httpclient"
	"github.com/javinizer/javinizer-go/internal/logging"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/javinizer/javinizer-go/internal/ratelimit"
	"github.com/javinizer/javinizer-go/internal/scraperutil"
	"golang.org/x/net/html/charset"
)

const defaultBaseURL = "https://dl.getchu.com"
const defaultIDPrefix = "getchu-"

var (
	itemIDRegex      = regexp.MustCompile(`(?i)^(?:getchu[-_]|item)?([0-9]+)$`)
	detailPathRegex  = regexp.MustCompile(`^/i/item([0-9]+)/?$`)
	numericIDRegex   = regexp.MustCompile(`^[0-9]+$`)
	productIDRegex   = regexp.MustCompile(`^作品ID\s*[:：]\s*([0-9]+)(?:\s*[／/]\s*サークルID[:：]\s*[0-9]+)?$`)
	descriptionRegex = regexp.MustCompile(`(?is)作品内容</td>(.*?)</td>`)
	releaseDateRegex = regexp.MustCompile(`(\d{4}/\d{2}/\d{2})`)
	runtimeRegex     = regexp.MustCompile(`([０-９\s]{1,3})分`)
	makerRegex       = regexp.MustCompile(`(?is)dojin_circle_detail\.php\?id=\d+[^>]*>([^<]+)</a>`)
	genreRegex       = regexp.MustCompile(`(?is)genre_id=\d+[^>]*>([^<]+)</a>`)
	coverRegex       = regexp.MustCompile(`(?i)(/data/item_img/[^\"']+/\d+top\.jpg)`)
	screenshotRegex  = regexp.MustCompile(`(?i)"(/data/item_img/[^\"']+\.(?:jpg|jpeg|webp))"\s+class="highslide"`)
	stripTagsRegex   = regexp.MustCompile(`(?s)<[^>]*>`)
)

// scraper implements the DLgetchu scraper.
type scraper struct {
	client        *resty.Client
	enabled       bool
	baseURL       string
	proxyOverride *models.ProxyConfig
	downloadProxy *models.ProxyConfig
	rateLimiter   *ratelimit.Limiter
	settings      models.ScraperSettings // stores the full settings for Config() method
}

// New creates a new DLgetchu scraper.
// newScraper creates a new DLgetchu scraper.
func newScraper(settings *models.ScraperSettings, globalProxy *models.ProxyConfig, globalFlareSolverr models.FlareSolverrConfig) *scraper {
	result := httpclient.InitScraperClient(settings, globalProxy, globalFlareSolverr,
		httpclient.WithScraperHeaders(httpclient.CombineHeaders(
			httpclient.StandardHTMLHeaders(),
			httpclient.UserAgentHeader(settings.UserAgent),
			map[string]string{"Accept-Language": "ja,en-US;q=0.8,en;q=0.6"},
		)),
	)
	client := result.Client

	base := strings.TrimSpace(settings.BaseURL)
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	s := &scraper{
		client:        client,
		enabled:       settings.Enabled,
		baseURL:       base,
		proxyOverride: settings.Proxy,
		downloadProxy: settings.DownloadProxy,
		rateLimiter:   ratelimit.NewLimiter(time.Duration(settings.RateLimit) * time.Millisecond),
		settings:      *settings,
	}

	if result.ProxyEnabled && strings.TrimSpace(result.ProxyProfile.URL) != "" {
		logging.Infof("DLgetchu: Using proxy %s", httpclient.SanitizeProxyURL(result.ProxyProfile.URL))
	}

	return s
}

// Name returns scraper identifier.
func (s *scraper) Name() string { return "dlgetchu" }

// IsEnabled returns whether scraper is enabled.
func (s *scraper) IsEnabled() bool { return s.enabled }

// Config returns the scraper's configuration
func (s *scraper) Config() *models.ScraperSettings {
	cloned := s.settings.Clone()
	return &cloned
}

// Close cleans up resources held by the scraper
func (s *scraper) Close() error {
	return nil
}

// ResolveDownloadProxyForHost declares DLgetchu-owned media hosts for downloader proxy routing.
func (s *scraper) ResolveDownloadProxyForHost(host string) (*models.ProxyConfig, *models.ProxyConfig, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, nil, false
	}
	if host == "dl.getchu.com" || strings.HasSuffix(host, ".dl.getchu.com") ||
		host == "getchu.com" || strings.HasSuffix(host, ".getchu.com") {
		return s.settings.DownloadProxy, s.settings.Proxy, true
	}
	return nil, nil, false
}

// CanHandleURL accepts only DLGetchu product URLs, not the separate retail catalog.
func (s *scraper) CanHandleURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	base, _ := url.Parse(s.baseURL)
	trustedHost := strings.EqualFold(u.Hostname(), "dl.getchu.com") && (u.Port() == "" || (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80"))
	if base != nil && base.Host != "" {
		trustedHost = trustedHost || strings.EqualFold(u.Host, base.Host)
	}
	return trustedHost && s.detailURLID(u) != ""
}

// detailURLID supports an administrator-configured reverse-proxy mount without
// treating the same path prefix on other origins as a valid product route.
func (s *scraper) detailURLID(u *url.URL) string {
	local := *u
	base, err := url.Parse(s.baseURL)
	if err == nil && strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host) {
		basePath := strings.TrimRight(base.Path, "/")
		if basePath != "" && strings.HasPrefix(local.Path, basePath+"/") {
			local.Path = strings.TrimPrefix(local.Path, basePath)
		}
	}
	return detailURLID(&local)
}

// detailURLID deliberately recognizes product routes only. Circle, genre and
// recommendation query parameters are not movie IDs.
func detailURLID(u *url.URL) string {
	if m := detailPathRegex.FindStringSubmatch(u.Path); len(m) == 2 {
		return m[1]
	}
	switch u.Path {
	case "/dc/doujin/doujin_detail.php", "/item":
	case "/index.php":
		if u.Query().Get("action") != "article" {
			return ""
		}
	default:
		return ""
	}
	ids := u.Query()["id"]
	if len(ids) == 1 && numericIDRegex.MatchString(ids[0]) {
		return ids[0]
	}
	return ""
}

func (s *scraper) ExtractIDFromURL(rawURL string) (string, error) {
	if !s.CanHandleURL(rawURL) {
		return "", fmt.Errorf("invalid DLgetchu product URL")
	}
	u, _ := url.Parse(rawURL)
	return s.detailURLID(u), nil
}

func (s *scraper) outputPrefix() string {
	if s.settings.IDPrefix != nil {
		return *s.settings.IDPrefix
	}
	return defaultIDPrefix
}

func (s *scraper) inputID(input string) string {
	input = strings.TrimSpace(input)
	if prefix := s.outputPrefix(); prefix != "" && len(input) > len(prefix) && strings.EqualFold(input[:len(prefix)], prefix) {
		if id := input[len(prefix):]; numericIDRegex.MatchString(id) {
			return id
		}
	}
	return extractNumericID(input)
}

func (s *scraper) ScrapeURL(ctx context.Context, rawURL string) (*models.ScraperResult, error) {
	id, err := s.ExtractIDFromURL(rawURL)
	if err != nil {
		return nil, models.NewScraperNotFoundError("DLgetchu", "URL not handled by DLgetchu scraper")
	}
	return s.scrapeDetail(ctx, rawURL, id)
}

func (s *scraper) scrapeDetail(ctx context.Context, rawURL, expectedID string) (*models.ScraperResult, error) {
	html, status, finalURL, err := s.fetchPageResponse(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch DLgetchu detail page: %w", err)
	}
	if status == http.StatusNotFound {
		return nil, models.NewScraperNotFoundError("DLgetchu", "page not found")
	}
	if status != http.StatusOK {
		return nil, models.NewScraperStatusError("DLgetchu", status, fmt.Sprintf("DLgetchu returned status code %d", status))
	}
	actualID, err := s.ExtractIDFromURL(finalURL)
	if err != nil || actualID != expectedID {
		return nil, models.NewScraperNotFoundError("DLgetchu", "redirected away from the requested product")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("failed to parse DLgetchu detail page: %w", err)
	}
	if !s.matchesProduct(doc, expectedID) {
		return nil, models.NewScraperNotFoundError("DLgetchu", "page does not identify the requested product")
	}
	result := parseDetailPage(doc, html, finalURL)
	result.ContentID = expectedID
	result.ID = s.outputPrefix() + expectedID
	return result, nil
}

// matchesProduct uses product-specific identity evidence, never a generic id=
// match or a caller-supplied fallback. A title and product body distinguish an
// actual detail page from an HTTP-200 home, search or age-confirmation page.
func (s *scraper) matchesProduct(doc *goquery.Document, expectedID string) bool {
	verified, mismatch, labelled := false, false, false
	doc.Find("meta[property='og:url'], link[rel='canonical']").Each(func(_ int, n *goquery.Selection) {
		raw := n.AttrOr("content", n.AttrOr("href", ""))
		id, err := s.ExtractIDFromURL(raw)
		if err != nil || id != expectedID {
			mismatch = true
		} else {
			verified = true
		}
	})
	doc.Find("td, div, p, span").Each(func(_ int, n *goquery.Selection) {
		if m := productIDRegex.FindStringSubmatch(scraperutil.CleanString(n.Text())); len(m) == 2 {
			labelled = true
			if m[1] != expectedID {
				mismatch = true
			} else {
				verified = true
			}
		}
	})
	hasDetails := false
	doc.Find("tr").Each(func(_ int, row *goquery.Selection) {
		cells := row.ChildrenFiltered("td")
		if cells.Length() < 2 {
			return
		}
		switch scraperutil.CleanString(cells.First().Text()) {
		case "サークル", "配信開始日", "作品内容":
			if scraperutil.CleanString(cells.Eq(1).Text()) != "" {
				hasDetails = true
			}
		}
	})
	title := scraperutil.CleanString(doc.Find("meta[property='og:title']").AttrOr("content", ""))
	if title == "" {
		title = scraperutil.CleanString(doc.Find("title").Text())
	}
	return verified && !mismatch && title != "" && (labelled || hasDetails)
}

// GetURL resolves only a verified, exact product match.
func (s *scraper) GetURL(ctx context.Context, id string) (string, error) { return s.getURLCtx(ctx, id) }

func (s *scraper) getURLCtx(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("movie ID cannot be empty")
	}
	if scraperutil.IsHTTPURL(id) {
		if !s.CanHandleURL(id) {
			return "", models.NewScraperNotFoundError("DLgetchu", "invalid product URL")
		}
		return id, nil
	}
	numericID := s.inputID(id)
	if numericID == "" {
		return "", models.NewScraperNotFoundError("DLgetchu", "unsupported product ID")
	}
	candidate := fmt.Sprintf("%s/i/item%s", s.baseURL, numericID)
	if result, err := s.scrapeDetail(ctx, candidate, numericID); err == nil {
		return result.SourceURL, nil
	} else if failure, ok := models.AsScraperError(err); !ok || failure.Kind != models.ScraperErrorKindNotFound {
		return "", err
	}
	for _, route := range []string{"/", "/gcosin/", "/gcosl/"} {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		searchURL := fmt.Sprintf("%s%s?search_keyword=%s", s.baseURL, route, url.QueryEscape(numericID))
		html, status, err := s.fetchPageCtx(ctx, searchURL)
		if err != nil {
			return "", err
		}
		if status == http.StatusNotFound {
			continue
		}
		if status != http.StatusOK {
			return "", models.NewScraperStatusError("DLgetchu", status, "DLgetchu search request failed")
		}
		if link := s.findDetailLink(html, numericID); link != "" {
			if result, err := s.scrapeDetail(ctx, link, numericID); err == nil {
				return result.SourceURL, nil
			} else if failure, ok := models.AsScraperError(err); !ok || failure.Kind != models.ScraperErrorKindNotFound {
				return "", err
			}
		}
	}
	return "", models.NewScraperNotFoundError("DLgetchu", fmt.Sprintf("movie %s not found on DLgetchu", id))
}

// Search scrapes metadata from DLgetchu without changing the caller's source ordering.
func (s *scraper) Search(ctx context.Context, id string) (*models.ScraperResult, error) {
	if !s.enabled {
		return nil, fmt.Errorf("DLgetchu scraper is disabled")
	}
	detailURL, err := s.getURLCtx(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.ScrapeURL(ctx, detailURL)
}

// ParseHTML parses a DL.Getchu detail page from a goquery.Document and the
// original raw HTML string. The raw HTML is needed because some extraction
// functions use regex on the source string rather than DOM queries.
// This is the documented parsing seam for testing.
func ParseHTML(doc *goquery.Document, html, sourceURL string) *models.ScraperResult {
	return parseDetailPage(doc, html, sourceURL)
}

func parseDetailPage(doc *goquery.Document, html, sourceURL string) *models.ScraperResult {
	result := &models.ScraperResult{
		Source:    "dlgetchu",
		SourceURL: sourceURL,
		Language:  "ja",
	}

	// Identity comes from the verified detail URL, never arbitrary IDs in HTML.
	if u, err := url.Parse(sourceURL); err == nil {
		result.ID = detailURLID(u)
	}
	result.ContentID = result.ID

	title := scraperutil.CleanString(doc.Find("meta[property='og:title']").AttrOr("content", ""))
	if title == "" {
		title = scraperutil.CleanString(doc.Find("title").First().Text())
	}
	result.Title = title
	result.OriginalTitle = title

	if m := releaseDateRegex.FindStringSubmatch(html); len(m) > 1 {
		raw := strings.ReplaceAll(m[1], "/", "-")
		if t, err := time.Parse("2006-01-02", raw); err == nil {
			result.ReleaseDate = &t
		}
	}

	if m := runtimeRegex.FindStringSubmatch(html); len(m) > 1 {
		raw := normalizeFullWidthDigits(m[1])
		if v, err := strconv.Atoi(strings.TrimSpace(strings.ReplaceAll(raw, " ", ""))); err == nil {
			result.Runtime = v
		}
	}

	if m := descriptionRegex.FindStringSubmatch(html); len(m) > 1 {
		result.Description = scraperutil.CleanString(stripTags(m[1]))
	}
	if result.Description == "" {
		result.Description = scraperutil.CleanString(doc.Find("meta[name='description']").AttrOr("content", ""))
	}

	if m := makerRegex.FindStringSubmatch(html); len(m) > 1 {
		result.Maker = scraperutil.CleanString(stripTags(m[1]))
	}

	result.Genres = extractGenres(html)
	doc.Find("tr").Each(func(_ int, row *goquery.Selection) {
		cells := row.ChildrenFiltered("td")
		if cells.Length() < 2 {
			return
		}
		value := scraperutil.CleanString(cells.Eq(1).Text())
		switch scraperutil.CleanString(cells.First().Text()) {
		case "サークル":
			result.Maker = value
		case "作品内容":
			result.Description = value
		case "配信開始日":
			if date := releaseDateRegex.FindString(value); date != "" {
				if parsed, err := time.Parse("2006/01/02", date); err == nil {
					result.ReleaseDate = &parsed
				}
			}
		}
	})

	if m := coverRegex.FindStringSubmatch(html); len(m) > 1 {
		result.CoverURL = scraperutil.ResolveURL(sourceURL, m[1])
	}
	result.PosterURL = result.CoverURL
	result.ScreenshotURL = extractScreenshots(html, sourceURL)
	result.ShouldCropPoster = true

	if result.Title == "" {
		result.Title = result.ID
		result.OriginalTitle = result.ID
	}

	return result
}

func extractGenres(html string) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, m := range genreRegex.FindAllStringSubmatch(html, -1) {
		if len(m) <= 1 {
			continue
		}
		g := scraperutil.CleanString(stripTags(m[1]))
		if g == "" || seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

func extractScreenshots(html, base string) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, m := range screenshotRegex.FindAllStringSubmatch(html, -1) {
		if len(m) <= 1 {
			continue
		}
		u := scraperutil.ResolveURL(base, m[1])
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func (s *scraper) findDetailLink(html, expectedID string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}
	found := ""
	doc.Find("a[href]").EachWithBreak(func(_ int, n *goquery.Selection) bool {
		base, parseErr := url.Parse(strings.TrimRight(s.baseURL, "/") + "/")
		ref, refErr := url.Parse(n.AttrOr("href", ""))
		if parseErr != nil || refErr != nil {
			return true
		}
		link := base.ResolveReference(ref).String()
		if id, err := s.ExtractIDFromURL(link); err == nil && id == expectedID {
			found = link
			return false
		}
		return true
	})
	return found
}

func extractNumericID(v string) string {
	if m := itemIDRegex.FindStringSubmatch(strings.TrimSpace(v)); len(m) == 2 {
		return m[1]
	}
	return ""
}

func normalizeFullWidthDigits(v string) string {
	replacer := strings.NewReplacer(
		"０", "0",
		"１", "1",
		"２", "2",
		"３", "3",
		"４", "4",
		"５", "5",
		"６", "6",
		"７", "7",
		"８", "8",
		"９", "9",
	)
	return replacer.Replace(v)
}

func (s *scraper) fetchPageCtx(ctx context.Context, targetURL string) (string, int, error) {
	body, status, _, err := s.fetchPageResponse(ctx, targetURL)
	return body, status, err
}

func (s *scraper) fetchPageResponse(ctx context.Context, targetURL string) (string, int, string, error) {
	if err := s.rateLimiter.Wait(ctx); err != nil {
		return "", 0, "", err
	}
	resp, err := s.client.R().SetContext(ctx).Get(targetURL)
	if err != nil {
		return "", 0, "", err
	}
	finalURL := targetURL
	if resp.RawResponse != nil && resp.RawResponse.Request != nil && resp.RawResponse.Request.URL != nil {
		finalURL = resp.RawResponse.Request.URL.String()
	}
	decoded, err := decodeBody(resp)
	if err != nil {
		decoded = resp.String()
	}
	if resp.StatusCode() == http.StatusOK && challengedetect.IsCloudflareChallengePage(decoded) {
		return "", resp.StatusCode(), finalURL, models.NewScraperChallengeError("DLgetchu", "DLgetchu returned a Cloudflare challenge page (request blocked; adjust proxy/IP)")
	}
	return decoded, resp.StatusCode(), finalURL, nil
}

func decodeBody(resp *resty.Response) (string, error) {
	body := resp.Body()
	if len(body) == 0 {
		return "", nil
	}
	contentType := resp.Header().Get("Content-Type")
	enc, _, _ := charset.DetermineEncoding(body, contentType)
	decoded, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func stripTags(v string) string {
	return stripTagsRegex.ReplaceAllString(v, "")
}
