package actress

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/javinizer/javinizer-go/internal/database"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/stretchr/testify/require"
)

func TestRegisteredDeleteActressRemovesTranslationsWithForeignKeysOnOrOff(t *testing.T) {
	for _, foreignKeys := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign keys off", true: "foreign keys on"}[foreignKeys], func(t *testing.T) {
			dsn := filepath.Join(t.TempDir(), "api-delete.db") + map[bool]string{false: "?_foreign_keys=0", true: "?_foreign_keys=1"}[foreignKeys]
			db, err := database.New(&database.Config{Type: "sqlite", DSN: dsn, LogLevel: "silent"})
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			require.NoError(t, db.RunMigrationsOnStartup(t.Context()))
			repos := db.Repositories()

			actress := models.Actress{JapaneseName: "API Delete", Verified: true, Origin: database.ActressOriginUser}
			require.NoError(t, db.Create(&actress).Error)
			require.NoError(t, db.Create(&models.ActressTranslation{ActressID: actress.ID, Language: "en", DisplayName: "API Delete"}).Error)

			gin.SetMode(gin.TestMode)
			router := gin.New()
			RegisterRoutes(router.Group("/api/v1"), NewActressDeps(repos.ContentRepos, repos.TranslationRepos))
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodDelete, "/api/v1/actresses/"+itoa(actress.ID), nil)
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())

			for table, predicate := range map[string]string{"actresses": "id = ?", "actress_translations": "actress_id = ?"} {
				var count int64
				require.NoError(t, db.Table(table).Where(predicate, actress.ID).Count(&count).Error)
				require.Zero(t, count, table)
			}
		})
	}
}
