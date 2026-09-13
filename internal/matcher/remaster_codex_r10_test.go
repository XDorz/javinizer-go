package matcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Codex round-10: a resolution tag after the remaster marker is not a codec
// spelling, so the marker survives (IPX-535-H-720p); and a quality label
// before the real catalog id must be suppressed for the same catalog grammar
// the matcher accepts, with the id then extracted from the remainder.
func TestRemasterMarkerResolutionAndQualityLabels(t *testing.T) {
	m, err := NewMatcher(&Config{})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, id string
	}{
		// Resolution tags after the marker keep the marker.
		{"IPX-535-H-720p.mkv", "IPX-535H"},
		{"IPX-535-HD-480p.mkv", "IPX-535H"},
		{"IPX-535-H-1080p.mkv", "IPX-535H"},
		// Codec tags still stay ambiguous.
		{"IPX-535-H.264.mkv", "IPX-535"},
		{"IPX-535-H264.mkv", "IPX-535"},
		// Leading quality labels suppress in favor of the trailing catalog id,
		// for every grammar the matcher accepts (separated, one-letter series,
		// long hyphenated series, separated T28, compact three-digit).
		{"FHD 1080 HD ABC.123.HD.mkv", "ABC-123H"},
		{"FHD 1080 HD A-123-HD.mkv", "A-123H"},
		{"FHD 1080 HD ABEAUTY-123-HD.mkv", "ABEAUTY-123H"},
		{"FHD 1080 HD T28.123.HD.mkv", "T28-123H"},
		{"FHD 1080 HD T28 123 HD.mkv", "T28-123H"},
		{"QUALITY 1080 HD AB123.mkv", "AB123"},
		{"QUALITY 1080 HD ABC123.mkv", "ABC123"},
		{"QUALITY 1080 HD A123.mkv", "A123"},
		{"QUALITY 1080 HD ABCDEFGHI.123.HD.mkv", "ABCDEFGHI-123H"},
		{"1080p IPX-535-H-720p.mkv", "IPX-535H"},
		// Ordinary hyphen-number tags after a real separated remaster never
		// suppress it, whatever the fallback matcher would pick instead.
		{"ABC.123.HD FHD-1080.mkv", "ABC-123H"},
		{"ABC.123.HD scene-2.mkv", "ABC-123H"},
		// Plain forms are unchanged.
		{"ABC.123.HD.mkv", "ABC-123H"},
		{"RCT156H.mkv", "RCT-156H"},
		{"RCT156H-720p.mkv", "RCT-156H"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.id, m.MatchString(tc.name))
		})
	}
}
