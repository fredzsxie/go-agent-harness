package config

import "testing"

func TestLoadLLMConfigReadsOptionalFallbackModel(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("MODEL_ID", "primary-model")
	t.Setenv("FALLBACK_MODEL_ID", " fallback-model")

	cfg, err := LoadLLMConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "primary-model" || cfg.FallbackModel != "fallback-model" {
		t.Fatalf("unexpected model config: %#v", cfg)
	}
}
