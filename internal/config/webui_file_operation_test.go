package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebUIFileOperationValidation(t *testing.T) {
	for name, validate := range map[string]func(*Config) error{
		"startup":  ValidateConfig,
		"settings": validateConfigExcludingTranslationCredentials,
	} {
		t.Run(name, func(t *testing.T) {
			for _, operation := range []string{"", "move", "copy", "hardlink", "softlink", "hard", "invalid", "MOVE"} {
				t.Run(operation, func(t *testing.T) {
					cfg := DefaultConfig(nil, nil)
					cfg.WebUI.DefaultFileOperation = operation
					err := validate(cfg)
					if operation == "hard" || operation == "invalid" || operation == "MOVE" {
						require.ErrorContains(t, err, "webui.default_file_operation")
					} else {
						require.NoError(t, err)
					}
				})
			}
		})
	}
}

func TestWebUIFileOperationCompatibilityAndPersistence(t *testing.T) {
	legacy, err := decodeConfig([]byte("webui:\n  default_review_view: detail\n"))
	require.NoError(t, err)
	assert.Equal(t, "move", legacy.WebUI.DefaultFileOperation)
	defaults := DefaultConfig(nil, nil)

	for _, operation := range []string{"move", "copy", "hardlink", "softlink"} {
		t.Run(operation, func(t *testing.T) {
			cfg := DefaultConfig(nil, nil)
			require.NoError(t, json.Unmarshal([]byte(`{"webui":{"default_file_operation":"`+operation+`"}}`), cfg))
			path := t.TempDir() + "/config.yaml"
			require.NoError(t, Save(cfg, path))
			reloaded, err := Load(path)
			require.NoError(t, err)
			assert.Equal(t, operation, reloaded.WebUI.DefaultFileOperation)
			assert.Equal(t, defaults.Output.Operation, reloaded.Output.Operation, "Web UI preference must not change CLI/TUI output defaults")
			data, err := json.Marshal(reloaded)
			require.NoError(t, err)
			assert.Contains(t, string(data), `"default_file_operation":"`+operation+`"`)
		})
	}
}
