package datamodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
