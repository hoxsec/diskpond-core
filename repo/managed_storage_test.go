package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/kopia/kopia/repo/blob"
)

func TestManagedStoreRejectsLegacyAndRedactsErrors(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "legacy.config")
	require.NoError(t, os.WriteFile(filename, []byte(`{"username":"u","hostname":"h"}`), 0o600))
	_, err := NewManagedConfigStore(filename).Load()
	require.ErrorContains(t, err, "requires desktop migration")
	ci := blob.ConnectionInfo{Type: "webdav", Config: map[string]string{"url": "https://fixture-user:fixture-password@example.com/repo?token=fixture-token", "password": "provider-secret"}}
	redact := connectionRedactor(ci)
	err = redact(errors.Wrap(blob.ErrBlobNotFound, "fixture-password fixture-token provider-secret"))
	require.ErrorIs(t, err, blob.ErrBlobNotFound)
	require.NotContains(t, fmt.Sprintf("%+v", err), "fixture-password")
	require.NotContains(t, err.Error(), "fixture-token")
	require.NotContains(t, err.Error(), "provider-secret")
}
