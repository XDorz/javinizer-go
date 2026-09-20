package dlgetchu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/japanese"
	"gopkg.in/yaml.v3"
)

// Neutral fixture based on the public DLGetchu product layout: og:url, a main
// ID/circle-ID label and labelled metadata rows, surrounded by unrelated links.
func productHTML(base, id string) string {
	return fmt.Sprintf(`<html><head><meta property="og:title" content="サンプル作品"><meta property="og:url" content="%s/i/item%s"></head><body>
 <a href="/circle/dojin_circle_detail.php?id=7777777">Circle navigation</a>
 <a href="/i/item8888888">Recommendation</a>
 <table><tr><td><b>作品ID：%s／サークルID：7777777</b></td></tr>
 <tr><td>サークル</td><td>Sample circle</td></tr>
 <tr><td>配信開始日</td><td>2026/09/01</td></tr>
 <tr><td>作品内容</td><td>サンプルの説明</td></tr></table>
 <input type="hidden" name="id" value="%s">
 </body></html>`, base, id, id, id)
}

func TestIdentityAndPrefix(t *testing.T) {
	const id = "1234567890123"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/i/item"+id, r.URL.Path)
		// Exercise the real site's EUC-JP decoding, not only UTF-8 fixtures.
		encoded, err := japanese.EUCJP.NewEncoder().Bytes([]byte(productHTML(server.URL, id)))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "text/html; charset=EUC-JP")
		_, _ = w.Write(encoded)
	}))
	defer server.Close()
	for _, input := range []string{id, "getchu-" + id, "GeTcHu_" + id, "ITEM" + id, server.URL + "/i/item" + id} {
		t.Run(input, func(t *testing.T) {
			s := newScraper(testSettings(server.URL), nil, models.FlareSolverrConfig{})
			result, err := s.Search(context.Background(), input)
			require.NoError(t, err)
			assert.Equal(t, "getchu-"+id, result.ID)
			assert.Equal(t, id, result.ContentID)
			assert.Equal(t, "サンプル作品", result.Title)
		})
	}
	for _, prefix := range []string{"", "dl_custom-", "getchu-123-", "item123_"} {
		settings := testSettings(server.URL)
		settings.IDPrefix = &prefix
		s := newScraper(settings, nil, models.FlareSolverrConfig{})
		result, err := s.Search(context.Background(), prefix+id)
		require.NoError(t, err)
		assert.Equal(t, prefix+id, result.ID)
		assert.Equal(t, id, result.ContentID)
	}
}

func TestRejectsWrongIdentityAndNonProductPages(t *testing.T) {
	for _, kind := range []string{"wrong_og_url", "wrong_main_label", "wrong_redirect", "search_redirect", "age_confirmation", "home_page", "recommendation_only"} {
		t.Run(kind, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if strings.Contains(r.URL.RawQuery, "search_keyword") {
					_, _ = fmt.Fprint(w, `<a href="/i/item54321">Unrelated result</a>`)
					return
				}
				body := productHTML(server.URL, "12345")
				switch kind {
				case "wrong_og_url":
					body = strings.Replace(body, server.URL+"/i/item12345", server.URL+"/i/item54321", 1)
				case "wrong_main_label":
					body = strings.Replace(body, "作品ID：12345", "作品ID：54321", 1)
				case "wrong_redirect":
					if r.URL.Path == "/i/item12345" {
						http.Redirect(w, r, "/i/item54321", http.StatusFound)
						return
					}
					body = productHTML(server.URL, "54321")
				case "search_redirect":
					if r.URL.Path == "/i/item12345" {
						http.Redirect(w, r, "/search", http.StatusFound)
						return
					}
				case "age_confirmation":
					body = `<html><title>Age confirmation</title><form>Confirm</form></html>`
				case "home_page":
					body = `<html><title>DLGetchu</title><meta property="og:url" content="` + server.URL + `/"></html>`
				case "recommendation_only":
					body = `<html><title>Search</title><a href="/i/item12345">Recommendation</a></html>`
				}
				_, _ = fmt.Fprint(w, body)
			}))
			defer server.Close()
			s := newScraper(testSettings(server.URL), nil, models.FlareSolverrConfig{})
			result, err := s.Search(context.Background(), "getchu-12345")
			require.Error(t, err)
			assert.Nil(t, result)
			failure, ok := models.AsScraperError(err)
			require.True(t, ok)
			assert.Equal(t, models.ScraperErrorKindNotFound, failure.Kind)
		})
	}
}

func TestSearchFallbackRequiresExactID(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch {
		case r.URL.Path == "/i/item12345":
			http.NotFound(w, r)
		case r.URL.Query().Get("search_keyword") == "12345":
			_, _ = fmt.Fprint(w, `<a href="/i/item54321">Wrong first hit</a><a href="/dc/doujin/doujin_detail.php?id=12345">Exact hit</a>`)
		case r.URL.Path == "/dc/doujin/doujin_detail.php" && r.URL.Query().Get("id") == "12345":
			_, _ = fmt.Fprint(w, productHTML(server.URL, "12345"))
		default:
			t.Errorf("unexpected request %s", r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	s := newScraper(testSettings(server.URL), nil, models.FlareSolverrConfig{})
	result, err := s.Search(context.Background(), "getchu-12345")
	require.NoError(t, err)
	assert.Equal(t, "getchu-12345", result.ID)
	assert.Contains(t, result.SourceURL, "doujin_detail.php?id=12345")
}

func TestRejectsUnsupportedInputsBeforeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Errorf("unexpected request: %s", r.URL) }))
	defer server.Close()
	s := newScraper(testSettings(server.URL), nil, models.FlareSolverrConfig{})
	for _, id := range []string{"ABC-123", "getchu-123abc", "item123x", "getchu-abcde", "<div>id=12345</div>", "https://example.com/i/item12345", "https://dl.getchu.com:8123/i/item12345"} {
		_, err := s.Search(context.Background(), id)
		require.Error(t, err, id)
	}
	for _, raw := range []string{"https://dl.getchu.com/circle?id=12345", "https://dl.getchu.com/i/item123abc", "https://dl.getchu.com/item?id=12345&id=54321", "https://www.getchu.com/soft.phtml?id=12345", "https://dl.getchu.com/i/item 作品ID: 12345"} {
		_, err := s.ExtractIDFromURL(raw)
		require.Error(t, err, raw)
	}
}

func TestHTTPFailuresAndCancellationRemainClassified(t *testing.T) {
	for _, status := range []int{403, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; w.WriteHeader(status) }))
			defer server.Close()
			s := newScraper(testSettings(server.URL), nil, models.FlareSolverrConfig{})
			_, err := s.Search(context.Background(), "getchu-12345")
			failure, ok := models.AsScraperError(err)
			require.True(t, ok)
			assert.Equal(t, status, failure.StatusCode)
			assert.Equal(t, 1, count)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := newScraper(testSettings("http://127.0.0.1:1"), nil, models.FlareSolverrConfig{})
	_, err := s.Search(ctx, "12345")
	require.ErrorIs(t, err, context.Canceled)
}

func TestIDPrefixRoundTripValidationAndClone(t *testing.T) {
	defaults := models.ScraperSettings{}
	prefix := defaultIDPrefix
	defaults.IDPrefix = &prefix
	for _, input := range []string{`{}`, `{"id_prefix":""}`, `{"id_prefix":"custom_"}`} {
		var settings models.ScraperSettings
		require.NoError(t, json.Unmarshal([]byte(input), &settings))
		settings.MergeDefaultsFrom(defaults)
		require.NoError(t, validateScraperSettings(&settings))
		want := defaultIDPrefix
		if input == `{"id_prefix":""}` {
			want = ""
		} else if input == `{"id_prefix":"custom_"}` {
			want = "custom_"
		}
		require.NotNil(t, settings.IDPrefix)
		assert.Equal(t, want, *settings.IDPrefix)
		for _, format := range []string{"json", "yaml"} {
			var restored models.ScraperSettings
			if format == "json" {
				data, err := json.Marshal(&settings)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &restored))
			} else {
				data, err := yaml.Marshal(&settings)
				require.NoError(t, err)
				require.NoError(t, yaml.Unmarshal(data, &restored))
			}
			restored.MergeDefaultsFrom(defaults)
			require.NotNil(t, restored.IDPrefix)
			assert.Equal(t, want, *restored.IDPrefix)
		}
		cloned := settings.Clone()
		*cloned.IDPrefix = "changed-"
		assert.Equal(t, want, *settings.IDPrefix)
	}
	for _, prefix := range []string{"getchu-123", "GeTcHu_12", "ITEM12", "../getchu-", "a/b", "getchu\n", "123", "--", strings.Repeat("a", 33)} {
		settings := models.ScraperSettings{IDPrefix: &prefix}
		assert.Error(t, validateScraperSettings(&settings), prefix)
	}
}

func TestConfiguredReverseProxySubpath(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				switch {
				case r.URL.Path == "/getchu/i/item12345":
					if fallback {
						http.NotFound(w, r)
						return
					}
					_, _ = fmt.Fprint(w, productHTML(server.URL+"/getchu", "12345"))
				case fallback && r.URL.Query().Get("search_keyword") == "12345":
					_, _ = fmt.Fprint(w, `<a href="/getchu/i/item54321">Unrelated</a><a href="dc/doujin/doujin_detail.php?id=12345">Exact result</a>`)
				case fallback && r.URL.Path == "/getchu/dc/doujin/doujin_detail.php" && r.URL.Query().Get("id") == "12345":
					_, _ = fmt.Fprint(w, productHTML(server.URL+"/getchu", "12345"))
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			s := newScraper(testSettings(server.URL+"/getchu/"), nil, models.FlareSolverrConfig{})
			result, err := s.Search(context.Background(), "getchu-12345")
			require.NoError(t, err)
			require.Equal(t, "getchu-12345", result.ID)
			assert.Equal(t, "12345", result.ContentID)
			assert.Contains(t, result.SourceURL, "/getchu/")
			if !fallback {
				direct, err := s.ScrapeURL(context.Background(), server.URL+"/getchu/i/item12345")
				require.NoError(t, err)
				assert.Equal(t, result.ID, direct.ID)
			}
			for _, raw := range []string{server.URL + "/getchuevil/i/item12345", server.URL + "/unrelated/i/item12345", "https://dl.getchu.com/getchu/i/item12345", "https://example.com/getchu/i/item12345"} {
				assert.False(t, s.CanHandleURL(raw), raw)
			}
		})
	}
}
