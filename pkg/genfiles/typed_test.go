package genfiles_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/truvity/pulumi-pipeline/pkg/genfiles"
)

type vpc struct {
	ID string `yaml:"vpc-id"`
}

func (v *vpc) Validate() error {
	if v.ID == "" {
		return errors.New("vpc-id is empty")
	}

	return nil
}

func TestTyped(t *testing.T) {
	got, err := genfiles.Parse[vpc]("gen/k/vpc.yaml", []byte("vpc-id: v1\n"))
	require.NoError(t, err)
	assert.Equal(t, "v1", got.ID)

	_, err = genfiles.Parse[vpc]("gen/k/vpc.yaml", []byte("other: x\n"))
	require.ErrorContains(t, err, "vpc-id is empty")

	b, err := genfiles.Marshal[vpc]("p", &vpc{ID: "v2"})
	require.NoError(t, err)
	assert.Equal(t, "vpc-id: v2\n", string(b))

	_, err = genfiles.Marshal[vpc]("p", &vpc{})
	require.ErrorContains(t, err, "vpc-id is empty")

	b, err = genfiles.Canonical[vpc]("p", map[string]any{"vpc-id": "v3", "ignored": 1})
	require.NoError(t, err)
	assert.Equal(t, "vpc-id: v3\n", string(b))

	_, err = genfiles.Canonical[vpc]("p", map[string]any{})
	require.ErrorContains(t, err, "vpc-id is empty")
}
