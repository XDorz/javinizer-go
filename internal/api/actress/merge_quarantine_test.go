package actress

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/javinizer/javinizer-go/internal/database"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/stretchr/testify/require"
)

func TestRegisteredMergeRetainsCandidateAmbiguityAcrossRestartAndRetries(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "merge-ambiguity.db")
	open := func() *database.DB {
		db, err := database.New(&database.Config{Type: "sqlite", DSN: dsn, LogLevel: "silent"})
		require.NoError(t, err)
		require.NoError(t, db.RunMigrationsOnStartup(ctx))
		return db
	}
	movieInput := func(id string) *models.Movie {
		return &models.Movie{
			ContentID: id,
			ID:        id,
			Title:     "Merge ambiguity " + id,
			Credits: []models.MovieCredit{{
				MovieContentID: id,
				CreditedName:   "Same Person",
				Origin:         string(models.CreditOriginScrape),
				Scraped:        models.Actress{DMMID: 88201, FirstName: "Same", LastName: "Person"},
			}},
		}
	}
	postMerge := func(t *testing.T, repo database.ActressRepositoryInterface, targetID, sourceID uint) {
		t.Helper()
		gin.SetMode(gin.TestMode)
		router := gin.New()
		group := router.Group("/api/v1")
		RegisterRoutes(group, ActressDeps{ContentRepos: database.ContentRepos{ActressRepo: repo}})
		payload, err := json.Marshal(map[string]any{"target_id": targetID, "source_id": sourceID})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/actresses/merge", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	}

	db := open()
	canonical := models.Actress{FirstName: "Same", LastName: "Person", Verified: true, Origin: database.ActressOriginUser}
	require.NoError(t, db.Create(&canonical).Error)
	movies := database.NewMovieRepository(db)
	first, err := movies.Upsert(ctx, movieInput("merge-api-same"))
	require.NoError(t, err)
	require.Len(t, first.Credits, 1)
	candidateID := first.Credits[0].ActressID
	otherCandidate := models.Actress{FirstName: "Other", LastName: "Candidate", Origin: database.ActressOriginScrape}
	require.NoError(t, db.Create(&otherCandidate).Error)
	postMerge(t, database.NewActressRepository(db), candidateID, otherCandidate.ID)

	var survivor models.Actress
	require.NoError(t, db.First(&survivor, candidateID).Error)
	require.False(t, survivor.Verified)
	require.True(t, survivor.AmbiguityQuarantined)
	require.NoError(t, db.Close())

	db = open()
	t.Cleanup(func() { _ = db.Close() })
	movies = database.NewMovieRepository(db)
	resolved, outcome, err := database.ResolveActressIdentityTx(db.DB, &models.Actress{DMMID: 88201, FirstName: "Same", LastName: "Person"})
	require.NoError(t, err)
	require.Equal(t, database.ResolutionAmbiguous, outcome)
	require.Equal(t, candidateID, resolved.ID)

	same, err := movies.Upsert(ctx, movieInput("merge-api-same"))
	require.NoError(t, err)
	other, err := movies.Upsert(ctx, movieInput("merge-api-other"))
	require.NoError(t, err)
	for _, movie := range []*models.Movie{same, other} {
		require.Empty(t, movie.Actresses)
		require.Equal(t, candidateID, movie.Credits[0].ActressID)
		require.ErrorIs(t, movies.WithApplyArtifactPublicationFence(ctx, movie.ContentID, movie.RenderGeneration, func(*models.Movie) error { return nil }), database.ErrApplyArtifactPublicationBlocked)
	}
	counts, err := database.NewCreditCollisionRepository(db).CountOpenByMovieBatch(ctx, []string{same.ContentID, other.ContentID})
	require.NoError(t, err)
	require.EqualValues(t, 1, counts[same.ContentID])
	require.EqualValues(t, 1, counts[other.ContentID])

	postMerge(t, database.NewActressRepository(db), canonical.ID, candidateID)
	survivor = models.Actress{}
	require.NoError(t, db.First(&survivor, canonical.ID).Error)
	require.True(t, survivor.Verified)
	require.False(t, survivor.AmbiguityQuarantined)
	for _, contentID := range []string{same.ContentID, other.ContentID} {
		movie, err := movies.FindByContentID(ctx, contentID)
		require.NoError(t, err)
		require.Len(t, movie.Actresses, 1)
		require.Equal(t, canonical.ID, movie.Actresses[0].ID)
		require.NoError(t, movies.WithApplyArtifactPublicationFence(ctx, contentID, movie.RenderGeneration, func(*models.Movie) error { return nil }))
	}
	counts, err = database.NewCreditCollisionRepository(db).CountOpenByMovieBatch(ctx, []string{same.ContentID, other.ContentID})
	require.NoError(t, err)
	require.Zero(t, counts[same.ContentID])
	require.Zero(t, counts[other.ContentID])
}
