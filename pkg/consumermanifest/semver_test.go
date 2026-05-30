package consumermanifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/consumermanifest"
)

func TestParseConstraint_AnyForms(t *testing.T) {
	for _, s := range []string{"", "*", "latest"} {
		c, err := consumermanifest.ParseConstraint(s)
		require.NoError(t, err, "constraint %q should parse", s)
		assert.True(t, c.Matches("0.4.3"))
		assert.True(t, c.Matches("99.0.0"))
	}
}

func TestParseConstraint_Exact(t *testing.T) {
	c, err := consumermanifest.ParseConstraint("0.4.3")
	require.NoError(t, err)
	assert.True(t, c.Matches("0.4.3"))
	assert.False(t, c.Matches("0.4.4"))
	assert.False(t, c.Matches("0.5.0"))
}

func TestParseConstraint_Caret_0x_Semantics(t *testing.T) {
	c, err := consumermanifest.ParseConstraint("^0.4")
	require.NoError(t, err)
	assert.True(t, c.Matches("0.4.0"))
	assert.True(t, c.Matches("0.4.3"))
	assert.False(t, c.Matches("0.5.0"))
	assert.False(t, c.Matches("0.3.9"))
	assert.False(t, c.Matches("1.0.0"))
}

func TestParseConstraint_Caret_NonZeroMajor(t *testing.T) {
	c, err := consumermanifest.ParseConstraint("^1.2")
	require.NoError(t, err)
	assert.True(t, c.Matches("1.2.0"))
	assert.True(t, c.Matches("1.9.0"))
	assert.False(t, c.Matches("2.0.0"))
	assert.False(t, c.Matches("1.1.9"))
}

func TestParseConstraint_Tilde(t *testing.T) {
	c, err := consumermanifest.ParseConstraint("~0.4.1")
	require.NoError(t, err)
	assert.True(t, c.Matches("0.4.1"))
	assert.True(t, c.Matches("0.4.5"))
	assert.False(t, c.Matches("0.4.0"))
	assert.False(t, c.Matches("0.5.0"))
}

func TestParseConstraint_Malformed(t *testing.T) {
	_, err := consumermanifest.ParseConstraint("abc")
	assert.Error(t, err)
	_, err = consumermanifest.ParseConstraint("^abc")
	assert.Error(t, err)
	_, err = consumermanifest.ParseConstraint("1.2.3.4.5")
	assert.Error(t, err)
}

func TestHighestSatisfying(t *testing.T) {
	c, err := consumermanifest.ParseConstraint("^0.4")
	require.NoError(t, err)
	picked := c.HighestSatisfying([]string{"0.4.0", "0.4.3", "0.5.0"})
	assert.Equal(t, "0.4.3", picked)
}
