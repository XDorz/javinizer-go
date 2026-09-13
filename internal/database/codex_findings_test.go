package database

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/javinizer/javinizer-go/internal/models"
)

func TestMovieRepositoryFindByIDLoadsCredits(t *testing.T) {
	db, _, credit, _ := collisionFixture(t)
	repo := NewMovieRepository(db)
	movie, err := repo.FindByID(t.Context(), credit.MovieContentID)
	require.NoError(t, err)
	require.Len(t, movie.Credits, 1)
	require.Equal(t, credit.ID, movie.Credits[0].ID)
	require.NotNil(t, movie.Credits[0].Actress)
	injectDatabaseCallbackError(t, db, "query", "movie_credits", 1)
	_, err = repo.FindByID(t.Context(), credit.MovieContentID)
	require.Error(t, err)
}

func TestDeleteCreditRecordsTxErrors(t *testing.T) {
	t.Run("collision", func(t *testing.T) {
		db := newCreditTestDB(t)
		injectDatabaseCallbackError(t, db, "delete", "credit_collisions", 1)
		err := deleteCreditRecordsTx(db.DB, "movie_content_id = ?", "movie_content_id = ?", "movie", "movie movie")
		require.ErrorContains(t, err, "credit collisions")
	})
	t.Run("credit", func(t *testing.T) {
		db := newCreditTestDB(t)
		injectDatabaseCallbackError(t, db, "delete", "movie_credits", 1)
		err := deleteCreditRecordsTx(db.DB, "movie_content_id = ?", "movie_content_id = ?", "movie", "movie movie")
		require.ErrorContains(t, err, "movie credits")
	})
}

func TestMovieDeleteCreditCleanupErrorRollsBack(t *testing.T) {
	db, _, credit, _ := collisionFixture(t)
	injectDatabaseCallbackError(t, db, "delete", "credit_collisions", 1)
	err := NewMovieRepository(db).Delete(t.Context(), credit.MovieContentID)
	require.Error(t, err)
	var movie models.Movie
	require.NoError(t, db.First(&movie, "content_id = ?", credit.MovieContentID).Error)
}

func TestMovieDeleteRemovesCreditsAndCollisions(t *testing.T) {
	db, _, credit, _ := collisionFixture(t)
	require.NoError(t, NewMovieRepository(db).Delete(t.Context(), credit.MovieContentID))
	var credits, collisions int64
	require.NoError(t, db.Model(&models.MovieCredit{}).Count(&credits).Error)
	require.NoError(t, db.Model(&models.CreditCollision{}).Count(&collisions).Error)
	require.Zero(t, credits)
	require.Zero(t, collisions)
}

func TestActressDeleteRemovesCreditsAndCollisions(t *testing.T) {
	db, _, credit, _ := collisionFixture(t)
	require.NoError(t, NewActressRepository(db).Delete(t.Context(), credit.ActressID))
	var credits, collisions int64
	require.NoError(t, db.Model(&models.MovieCredit{}).Count(&credits).Error)
	require.NoError(t, db.Model(&models.CreditCollision{}).Count(&collisions).Error)
	require.Zero(t, credits)
	require.Zero(t, collisions)
}

func TestActressDeleteErrorsRollback(t *testing.T) {
	t.Run("cleanup", func(t *testing.T) {
		db, _, credit, _ := collisionFixture(t)
		injectDatabaseCallbackError(t, db, "delete", "movie_credits", 1)
		err := NewActressRepository(db).Delete(t.Context(), credit.ActressID)
		require.Error(t, err)
		var actress models.Actress
		require.NoError(t, db.First(&actress, credit.ActressID).Error)
	})
	t.Run("actress", func(t *testing.T) {
		db := newCreditTestDB(t)
		actress := models.Actress{FirstName: "Delete"}
		require.NoError(t, db.Create(&actress).Error)
		injectDatabaseCallbackError(t, db, "delete", "actresses", 1)
		err := NewActressRepository(db).Delete(t.Context(), actress.ID)
		require.Error(t, err)
	})
}
