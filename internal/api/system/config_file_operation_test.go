package system

import (
	"os"
	"testing"

	"github.com/javinizer/javinizer-go/internal/api/core"
	"github.com/javinizer/javinizer-go/internal/api/testkit"
	"github.com/javinizer/javinizer-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigUpdateService_FileOperation(t *testing.T) {
	oldCfg := config.DefaultConfig(nil, nil)
	path := t.TempDir() + "/config.yaml"
	require.NoError(t, config.Save(oldCfg, path))
	deps := createTestDeps(t, oldCfg, path)
	svc := NewConfigUpdateService(testkit.GetTestRuntime(deps), path)
	svc.reload = func(*core.APIRuntime, *core.APIDeps, *config.Config) error { return nil }

	updated := oldCfg.Clone()
	updated.WebUI.DefaultFileOperation = "hardlink"
	require.NoError(t, svc.ValidateAndApply(oldCfg, updated, nil))
	assert.Equal(t, "hardlink", deps.CoreDeps.GetConfig().WebUI.DefaultFileOperation)
	loaded, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "hardlink", loaded.WebUI.DefaultFileOperation)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	invalid := updated.Clone()
	invalid.WebUI.DefaultFileOperation = "invalid"
	err = svc.ValidateAndApply(updated, invalid, nil)
	require.ErrorContains(t, err, "webui.default_file_operation")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "invalid choices must not be persisted")
	assert.Equal(t, "hardlink", deps.CoreDeps.GetConfig().WebUI.DefaultFileOperation)
}
