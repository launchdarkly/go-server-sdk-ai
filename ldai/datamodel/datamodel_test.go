package datamodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenUsage_Set(t *testing.T) {
	tests := []struct {
		name  string
		usage TokenUsage
		want  bool
	}{
		{"zero value", TokenUsage{}, false},
		{"total only", TokenUsage{Total: 1}, true},
		{"input only", TokenUsage{Input: 1}, true},
		{"output only", TokenUsage{Output: 1}, true},
		{"all fields", TokenUsage{Total: 3, Input: 1, Output: 2}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.usage.Set())
		})
	}
}

func TestJudgeConfiguration_Clone_Nil(t *testing.T) {
	var jc *JudgeConfiguration
	assert.Nil(t, jc.Clone())
}

func TestJudgeConfiguration_Clone_DeepCopy(t *testing.T) {
	original := &JudgeConfiguration{
		Judges: []Judge{
			{Key: "judge1", SamplingRate: 0.1},
			{Key: "judge2", SamplingRate: 0.2},
		},
	}

	clone := original.Clone()
	require.NotNil(t, clone)
	assert.Equal(t, original.Judges, clone.Judges)

	// Mutating the original must not affect the clone (and vice versa).
	original.Judges[0].Key = "mutated"
	original.Judges = append(original.Judges, Judge{Key: "judge3"})

	require.Len(t, clone.Judges, 2)
	assert.Equal(t, "judge1", clone.Judges[0].Key)
}
