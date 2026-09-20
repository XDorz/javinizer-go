package matcher

import (
	"fmt"
	"strings"
	"testing"

	"github.com/javinizer/javinizer-go/internal/config"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDLGetchuFilenames(t *testing.T) {
	cfg := config.DefaultConfig(nil, nil)
	customPrefix := "dl_custom-X_"
	cfg.Scrapers.Overrides["dlgetchu"] = &models.ScraperSettings{IDPrefix: &customPrefix}
	for _, enabled := range []bool{false, true} {
		cfg.Matching.RegexEnabled = enabled
		m, err := NewMatcher(ConfigFromAppConfig(cfg))
		require.NoError(t, err)
		for _, tc := range []struct{ name, id string }{
			{"getchu-1234567890123.mp4", "GETCHU-1234567890123"},
			{"GeTcHu_1234567890123-C.mp4", "GETCHU_1234567890123"},
			{"iTeM1234567890123.mp4", "ITEM1234567890123"},
			{"dl_custom-X_1234567890123.mp4", "DL_CUSTOM-X_1234567890123"},
			{"[folder] getchu-1234567890123 [1080p].mkv", "GETCHU-1234567890123"},
			{"IPX-123.mp4", "IPX-123"},
		} {
			result := m.MatchFile(models.FileMatchInfo{Name: tc.name, Extension: ".mp4"})
			require.NotNil(t, result, tc.name)
			assert.Equal(t, tc.id, result.ID, tc.name)
			assert.Equal(t, tc.id, m.MatchString(tc.name))
		}
	}
}

func TestDLGetchuCustomPrefixBeforeCanonicalPrefix(t *testing.T) {
	for _, prefix := range []string{"getchu-123-", "item123_"} {
		m, err := NewMatcher(&Config{DLGetchuIDPrefix: prefix})
		require.NoError(t, err)
		name := prefix + "4567890.mp4"
		result := m.MatchFile(models.FileMatchInfo{Name: name, Extension: ".mp4"})
		require.NotNil(t, result)
		assert.Equal(t, strings.ToUpper(prefix)+"4567890", result.ID)
		assert.Equal(t, result.ID, m.MatchString(name))
	}
}

func TestDLGetchuMalformedTokensDoNotFallBackToPartialIDs(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, prefix := range []string{"", "getchu-123-", "item123_", "DL_custom-"} {
			cfg := config.DefaultConfig(nil, nil)
			m, err := NewMatcher(&Config{RegexEnabled: enabled, RegexPattern: cfg.Matching.RegexPattern, DLGetchuIDPrefix: prefix})
			require.NoError(t, err)
			names := []string{"getchu-123abc.mp4", "GeTcHu_1234567890123x.mp4", "GETCHU-1234567890123x.mp4", "item123abc.mp4", "getchu-123E.mp4", "getchu-getchu-123abc.mp4", "getchu-item123abc.mp4"}
			if prefix != "" {
				names = append(names, prefix+"456abc.mp4", prefix+"abc.mp4")
			}
			for _, name := range names {
				t.Run(fmt.Sprintf("%t/%s/%s", enabled, prefix, name), func(t *testing.T) {
					assert.Nil(t, m.MatchFile(models.FileMatchInfo{Name: name, Extension: ".mp4"}))
					assert.Empty(t, m.MatchString(name))
					// Only the malformed Getchu token is excluded from fallback matching.
					mixed := strings.TrimSuffix(name, ".mp4") + " IPX-535.mp4"
					result := m.MatchFile(models.FileMatchInfo{Name: mixed, Extension: ".mp4"})
					require.NotNil(t, result)
					assert.Equal(t, "IPX-535", result.ID)
					assert.Equal(t, "IPX-535", m.MatchString(mixed))
				})
			}
		}
	}
}

func TestDLGetchuValidSuffixesStillMatch(t *testing.T) {
	m, err := NewMatcher(&Config{})
	require.NoError(t, err)
	for _, suffix := range []string{"-C", "-pt1", "_1080p", " [1080p]", " 日本語タイトル"} {
		name := "getchu-1234567" + suffix + ".mp4"
		result := m.MatchFile(models.FileMatchInfo{Name: name, Extension: ".mp4"})
		require.NotNil(t, result)
		assert.Equal(t, "GETCHU-1234567", result.ID)
		assert.Equal(t, result.ID, m.MatchString(name))
		if suffix == "-pt1" {
			assert.True(t, result.IsMultiPart)
			assert.Equal(t, 1, result.PartNumber)
		}
	}
}

func TestDLGetchuDoesNotClaimOtherItemPrefixes(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := config.DefaultConfig(nil, nil)
		m, err := NewMatcher(&Config{RegexEnabled: enabled, RegexPattern: cfg.Matching.RegexPattern})
		require.NoError(t, err)
		for _, name := range []string{"ITEM-123", "ITEMS-123", "itemsABC-123"} {
			result := m.MatchFile(models.FileMatchInfo{Name: name + ".mp4", Extension: ".mp4"})
			require.NotNil(t, result, name)
			assert.Equal(t, strings.ToUpper(name), result.ID)
			assert.Equal(t, result.ID, m.MatchString(name))
		}
	}
}
