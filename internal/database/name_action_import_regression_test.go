package database

import (
	"context"
	"testing"

	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMovieUpsertCanonicalCreditedNameRepresentationsGateOnlyMismatch(t *testing.T) {
	tests := []struct {
		name             string
		actress          models.Actress
		creditedName     string
		creditedJapanese string
		acceptedAlias    string
		wantOpen         bool
	}{
		{name: "last first", actress: models.Actress{DMMID: 8101, FirstName: "Yui", LastName: "Hatano", JapaneseName: "波多野結衣"}, creditedName: "Hatano Yui"},
		{name: "first last", actress: models.Actress{DMMID: 8102, FirstName: "Yui", LastName: "Hatano", JapaneseName: "波多野結衣"}, creditedName: "Yui Hatano"},
		{name: "japanese", actress: models.Actress{DMMID: 8103, FirstName: "Yui", LastName: "Hatano", JapaneseName: "波多野結衣"}, creditedJapanese: "波多野結衣"},
		{name: "single component", actress: models.Actress{DMMID: 8104, FirstName: "Yui"}, creditedName: "Yui"},
		{name: "empty", actress: models.Actress{DMMID: 8105, FirstName: "Yui", LastName: "Hatano"}},
		{name: "normalized whitespace and case", actress: models.Actress{DMMID: 8106, FirstName: "Yui", LastName: "Hatano"}, creditedName: "  yUi   hAtAnO  "},
		{name: "persisted alias", actress: models.Actress{DMMID: 8107, FirstName: "Yui", LastName: "Hatano"}, creditedName: "Stage Name", acceptedAlias: "Stage Name"},
		{name: "true mismatch", actress: models.Actress{DMMID: 8108, FirstName: "Yui", LastName: "Hatano"}, creditedName: "Someone Else", wantOpen: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newCreditTestDB(t)
			tc.actress.Verified = true
			tc.actress.Origin = ActressOriginUser
			require.NoError(t, db.Create(&tc.actress).Error)
			if tc.acceptedAlias != "" {
				require.NoError(t, db.Create(&models.ActressAlias{AliasName: tc.acceptedAlias, CanonicalName: tc.actress.FullName()}).Error)
			}
			movie := &models.Movie{ContentID: "canonical-gate", ID: "canonical-gate", Credits: []models.MovieCredit{{
				MovieContentID: "canonical-gate", CreditedName: tc.creditedName,
				CreditedJapaneseName: tc.creditedJapanese, Source: "dmm",
				Scraped: models.Actress{DMMID: tc.actress.DMMID},
			}}}
			repo := NewMovieRepository(db)
			_, err := repo.UpsertWithTranslations(context.Background(), movie, nil, nil)
			require.NoError(t, err)
			open, err := NewCreditCollisionRepository(db).ListOpenByMovie(context.Background(), movie.ContentID)
			require.NoError(t, err)
			if tc.wantOpen {
				require.Len(t, open, 1)
				require.Equal(t, models.CreditFieldCreditedName, open[0].Field)
			} else {
				require.Empty(t, open)
			}
		})
	}
}

func TestImportUpsertMatchesUnionOfCanonicalRepresentations(t *testing.T) {
	t.Run("changed Japanese with stable English", func(t *testing.T) {
		db := newCreditTestDB(t)
		repo := NewActressRepository(db)
		existing := models.Actress{FirstName: "Yui", LastName: "Hatano", JapaneseName: "波多野結衣", Verified: true, Origin: ActressOriginImport}
		require.NoError(t, db.Create(&existing).Error)
		incoming := models.Actress{FirstName: "Yui", LastName: "Hatano", JapaneseName: "新表記"}
		require.NoError(t, repo.ImportUpsert(context.Background(), &incoming))
		require.Equal(t, existing.ID, incoming.ID)
		var count int64
		require.NoError(t, db.Model(&models.Actress{}).Count(&count).Error)
		require.Equal(t, int64(1), count)
	})

	t.Run("reversed English order", func(t *testing.T) {
		db := newCreditTestDB(t)
		repo := NewActressRepository(db)
		existing := models.Actress{FirstName: "Yui", LastName: "Hatano", Verified: true, Origin: ActressOriginImport}
		require.NoError(t, db.Create(&existing).Error)
		incoming := models.Actress{FirstName: "Hatano", LastName: "Yui"}
		require.NoError(t, repo.ImportUpsert(context.Background(), &incoming))
		require.Equal(t, existing.ID, incoming.ID)
		var count int64
		require.NoError(t, db.Model(&models.Actress{}).Count(&count).Error)
		require.Equal(t, int64(1), count)
	})

	t.Run("conflicting representations are ambiguous without mutation", func(t *testing.T) {
		db := newCreditTestDB(t)
		repo := NewActressRepository(db)
		japaneseOwner := models.Actress{FirstName: "Other", LastName: "Person", JapaneseName: "競合名", Verified: true, Origin: ActressOriginUser}
		englishOwner := models.Actress{FirstName: "Yui", LastName: "Hatano", JapaneseName: "別名", Verified: true, Origin: ActressOriginUser}
		require.NoError(t, db.Create(&japaneseOwner).Error)
		require.NoError(t, db.Create(&englishOwner).Error)
		incoming := models.Actress{FirstName: "Yui", LastName: "Hatano", JapaneseName: "競合名"}
		err := repo.ImportUpsert(context.Background(), &incoming)
		require.ErrorContains(t, err, "ambiguous import match")
		require.Zero(t, incoming.ID)
		var actressCount, aliasCount int64
		require.NoError(t, db.Model(&models.Actress{}).Count(&actressCount).Error)
		require.NoError(t, db.Model(&models.ActressAlias{}).Count(&aliasCount).Error)
		require.Equal(t, int64(2), actressCount)
		require.Zero(t, aliasCount)
	})
}

func TestAllowedCollisionResolutionMatrixMatchesValidator(t *testing.T) {
	all := []string{
		models.CollisionResolutionKeepIdentity,
		models.CollisionResolutionAdoptCanonical,
		models.CollisionResolutionAdoptAlias,
		models.CollisionResolutionReassign,
	}
	tests := []struct {
		name     string
		field    string
		status   string
		verified bool
		want     []string
	}{
		{name: "credited verified", field: models.CreditFieldCreditedName, status: models.CollisionStatusOpen, verified: true, want: all},
		{name: "credited candidate", field: models.CreditFieldCreditedName, status: models.CollisionStatusOpen, want: all},
		{name: "thumb verified", field: models.CreditFieldReportedThumb, status: models.CollisionStatusOpen, verified: true, want: []string{models.CollisionResolutionKeepIdentity, models.CollisionResolutionAdoptCanonical, models.CollisionResolutionReassign}},
		{name: "thumb candidate", field: models.CreditFieldReportedThumb, status: models.CollisionStatusOpen, want: []string{models.CollisionResolutionKeepIdentity, models.CollisionResolutionAdoptCanonical, models.CollisionResolutionReassign}},
		{name: "identity verified", field: models.CreditFieldIdentityLink, status: models.CollisionStatusOpen, verified: true, want: []string{models.CollisionResolutionKeepIdentity, models.CollisionResolutionAdoptCanonical, models.CollisionResolutionReassign}},
		{name: "identity candidate", field: models.CreditFieldIdentityLink, status: models.CollisionStatusOpen, want: []string{models.CollisionResolutionAdoptCanonical, models.CollisionResolutionReassign}},
		{name: "closed row", field: models.CreditFieldCreditedName, status: models.CollisionStatusResolved, verified: true, want: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			collision := &models.CreditCollision{Field: tc.field, Status: tc.status}
			credit := &models.MovieCredit{Actress: &models.Actress{Verified: tc.verified}}
			allowed := AllowedCollisionResolutions(collision, credit)
			require.Equal(t, tc.want, allowed)
			allowedSet := make(map[string]bool, len(allowed))
			for _, resolution := range allowed {
				allowedSet[resolution] = true
			}
			for _, resolution := range all {
				targetID := uint(0)
				if resolution == models.CollisionResolutionReassign {
					targetID = 999
				}
				err := validateCollisionResolution(collision, credit, resolution, targetID)
				if allowedSet[resolution] {
					require.NoError(t, err, resolution)
				} else {
					require.Error(t, err, resolution)
				}
			}
		})
	}
}

func TestAllowedResolutionsErrorAndEmptyContracts(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		db := newCreditTestDB(t)
		got, err := NewCollisionService(db).AllowedResolutions(context.Background(), nil)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("query failure", func(t *testing.T) {
		db := newCreditTestDB(t)
		service := NewCollisionService(db)
		require.NoError(t, db.Close())
		_, err := service.AllowedResolutions(context.Background(), []models.CreditCollision{{ID: 1, CreditID: 1, Status: models.CollisionStatusOpen}})
		require.Error(t, err)
	})

	t.Run("missing credit", func(t *testing.T) {
		db := newCreditTestDB(t)
		_, err := NewCollisionService(db).AllowedResolutions(context.Background(), []models.CreditCollision{{ID: 1, CreditID: 999, Status: models.CollisionStatusOpen}})
		require.ErrorIs(t, err, ErrNotFound)
	})
}

func TestResolveCollisionUpdateFailureRollsBack(t *testing.T) {
	db, service, _, collision := collisionFixture(t)
	require.NoError(t, db.Exec("CREATE TRIGGER fail_allowed_resolution_update BEFORE UPDATE ON credit_collisions BEGIN SELECT RAISE(ABORT, 'injected collision update failure'); END").Error)
	_, err := service.resolveTx(db.DB, collision.ID, models.CollisionResolutionKeepIdentity, 0)
	require.Error(t, err)
	require.NoError(t, db.First(&collision, collision.ID).Error)
	require.Equal(t, models.CollisionStatusOpen, collision.Status)
}

func TestResolveCollisionLosesOpenStatusBeforeConditionalUpdate(t *testing.T) {
	db, service, _, collision := collisionFixture(t)
	callbackName := "test:close_collision_before_conditional_update"
	fired := false
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if fired || tx.Statement.Table != "credit_collisions" {
			return
		}
		fired = true
		_ = tx.Exec("UPDATE credit_collisions SET status = ? WHERE id = ?", models.CollisionStatusResolved, collision.ID).Error
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

	_, err := service.resolveTx(db.DB, collision.ID, models.CollisionResolutionKeepIdentity, 0)
	require.ErrorIs(t, err, ErrCollisionNotOpen)
	require.True(t, fired)
}
