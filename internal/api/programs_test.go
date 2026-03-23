package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/trustos/pulumi-ui/internal/programs"
)

func TestHasBlockingErrors_EmptySlice(t *testing.T) {
	assert.False(t, hasBlockingErrors(nil))
	assert.False(t, hasBlockingErrors([]programs.ValidationError{}))
}

func TestHasBlockingErrors_OnlyLevel7(t *testing.T) {
	errs := []programs.ValidationError{
		{Level: programs.LevelAgentAccess, Message: "no networking context"},
	}
	assert.False(t, hasBlockingErrors(errs), "Level 7 warnings should not block")
}

func TestHasBlockingErrors_Level5Blocks(t *testing.T) {
	errs := []programs.ValidationError{
		{Level: 5, Message: "missing required property"},
	}
	assert.True(t, hasBlockingErrors(errs))
}

func TestHasBlockingErrors_MixedLevels(t *testing.T) {
	errs := []programs.ValidationError{
		{Level: programs.LevelAgentAccess, Message: "warning only"},
		{Level: 3, Message: "structure error"},
	}
	assert.True(t, hasBlockingErrors(errs), "any error below Level 7 should block")
}

func TestHasBlockingErrors_AllLowLevels(t *testing.T) {
	for _, level := range []programs.ValidationLevel{1, 2, 3, 4, 5, 6} {
		errs := []programs.ValidationError{{Level: level, Message: "err"}}
		assert.True(t, hasBlockingErrors(errs), "Level %d should block", level)
	}
}
