package dlgetchu

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/javinizer/javinizer-go/internal/aggregator"
	"github.com/javinizer/javinizer-go/internal/config"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/javinizer/javinizer-go/internal/nfo"
	"github.com/javinizer/javinizer-go/internal/template"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifiedPrefixFlowsToNamingAndNFO(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, productHTML(server.URL, "1234567"))
	}))
	defer server.Close()
	s := newScraper(testSettings(server.URL), nil, models.FlareSolverrConfig{})
	result, err := s.Search(context.Background(), "item1234567")
	require.NoError(t, err)
	cfg := &config.Config{Scrapers: config.ScrapersConfig{Priority: []string{"dlgetchu"}}}
	agg := aggregator.New(aggregator.ConfigFromAppConfig(cfg), nil, nil, nil)
	movie, _, err := agg.Aggregate([]*models.ScraperResult{result})
	require.NoError(t, err)
	require.Equal(t, "getchu-1234567", movie.ID)
	require.Equal(t, "1234567", movie.ContentID)
	filename, err := template.NewEngine().Execute("<ID>.mp4", template.NewContextFromMovie(movie))
	require.NoError(t, err)
	assert.Equal(t, "getchu-1234567.mp4", filename)
	fs := afero.NewMemMapFs()
	generator := nfo.NewGenerator(fs, &nfo.Config{})
	require.NoError(t, generator.GenerateAtPath(context.Background(), movie, "/movie.nfo", "", nil))
	data, err := afero.ReadFile(fs, "/movie.nfo")
	require.NoError(t, err)
	assert.Contains(t, string(data), "<id>getchu-1234567</id>")
	assert.Contains(t, string(data), `<uniqueid type="contentid" default="true">1234567</uniqueid>`)
}
