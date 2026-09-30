package config

import "testing"

func TestLoadLLMConfigReadsOptionalFallbackModel(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("MODEL_ID", "primary-model")
	t.Setenv("FALLBACK_MODEL_ID", " fallback-model")
	t.Setenv("GOAL_EVALUATOR_MODEL_ID", "")
	t.Setenv("MAX_TURNS", "")
	t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "")

	cfg, err := LoadLLMConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "primary-model" || cfg.FallbackModel != "fallback-model" || cfg.GoalEvaluatorModel != "primary-model" {
		t.Fatalf("unexpected model config: %#v", cfg)
	}
	if cfg.MaxTurns != 0 || cfg.GoalStopBlockCap != DefaultGoalStopBlockCap {
		t.Fatalf("unexpected Goal limits: %#v", cfg)
	}
}

func TestLoadLLMConfigReadsGoalSettings(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("MODEL_ID", "main")
	t.Setenv("GOAL_EVALUATOR_MODEL_ID", "judge")
	t.Setenv("MAX_TURNS", "20")
	t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "3")

	cfg, err := LoadLLMConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GoalEvaluatorModel != "judge" || cfg.MaxTurns != 20 || cfg.GoalStopBlockCap != 3 {
		t.Fatalf("unexpected Goal config: %#v", cfg)
	}
}

func TestLoadLLMConfigRejectsInvalidGoalSettings(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "negative turns", key: "MAX_TURNS", value: "-1"},
		{name: "invalid turns", key: "MAX_TURNS", value: "many"},
		{name: "zero block cap", key: "CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", value: "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "test-key")
			t.Setenv("MAX_TURNS", "")
			t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "")
			t.Setenv(test.key, test.value)
			if _, err := LoadLLMConfig(); err == nil {
				t.Fatal("expected invalid Goal setting to fail")
			}
		})
	}
}
