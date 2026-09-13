package temp

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/require"
)

func TestPathWithinDirPR260(t *testing.T) {
	dir := filepath.Join("root", "posters")
	inside := filepath.Join(dir, "image.jpg")
	outside := filepath.Join("root", "outside.jpg")
	require.True(t, pathWithinDir(dir, inside))
	require.False(t, pathWithinDir(dir, outside))

	insideContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.False(t, rejectOutsideDir(insideContext, dir, inside))
	outsideRecorder := httptest.NewRecorder()
	outsideContext, _ := gin.CreateTestContext(outsideRecorder)
	require.True(t, rejectOutsideDir(outsideContext, dir, outside))
	require.Equal(t, 404, outsideRecorder.Code)
}
