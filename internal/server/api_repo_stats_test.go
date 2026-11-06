package server_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kopia/kopia/fs"
	"github.com/kopia/kopia/internal/apiclient"
	"github.com/kopia/kopia/internal/mockfs"
	"github.com/kopia/kopia/internal/repotesting"
	"github.com/kopia/kopia/internal/servertesting"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/upload"
)

func TestRepositoryStats(t *testing.T) {
	ctx, env := repotesting.NewEnvironment(t, repotesting.FormatNotImportant)
	content := bytes.Repeat([]byte("snapshot statistics\n"), 10000)
	const singleFileSize = 12
	require.NoError(t, repo.WriteSession(ctx, env.Repository, repo.WriteSessionOptions{Purpose: "Test"}, func(ctx context.Context, w repo.RepositoryWriter) error {
		dir := mockfs.NewDirectory()
		dir.AddFile("large.txt", content, 0o644)
		for i := range 3 {
			m, err := upload.NewUploader(w).Upload(ctx, dir, nil, env.LocalPathSourceInfo("/documents"))
			require.NoError(t, err)
			m.StartTime = fs.UTCTimestampFromTime(time.Now().Add(time.Duration(i) * time.Minute))
			if i == 2 {
				// A more recent incomplete snapshot must not replace the latest
				// complete copy in the protected-data counters.
				m.IncompleteReason = "canceled"
				m.RootEntry.DirSummary.TotalFileSize = 0
				m.RootEntry.DirSummary.TotalFileCount = 0
			}
			_, err = snapshot.SaveSnapshot(ctx, w, m)
			require.NoError(t, err)
		}
		file := mockfs.NewDirectory().AddFile("single.txt", bytes.Repeat([]byte("x"), singleFileSize), 0o644)
		m, err := upload.NewUploader(w).Upload(ctx, file, nil, env.LocalPathSourceInfo("/single.txt"))
		require.NoError(t, err)
		_, err = snapshot.SaveSnapshot(ctx, w, m)
		return err
	}))

	srv := servertesting.StartServer(t, env, false)
	client, err := apiclient.NewKopiaAPIClient(apiclient.Options{
		BaseURL: srv.BaseURL, TrustedServerCertificateFingerprint: srv.TrustedServerCertificateFingerprint,
		Username: servertesting.TestUIUsername, Password: servertesting.TestUIPassword,
	})
	require.NoError(t, err)
	require.NoError(t, client.FetchCSRFTokenForTesting(ctx))
	var result struct {
		ProtectedBytes int64  `json:"protectedBytes"`
		ProtectedFiles int64  `json:"protectedFiles"`
		Snapshots      int    `json:"snapshots"`
		LogicalBytes   int64  `json:"logicalBytes"`
		PackedBytes    *int64 `json:"packedBytes"`
		SavedBytes     *int64 `json:"savedBytes"`
	}
	require.NoError(t, client.Get(ctx, "repo/stats", nil, &result))
	require.EqualValues(t, len(content)+singleFileSize, result.ProtectedBytes)
	require.EqualValues(t, 2, result.ProtectedFiles)
	require.Equal(t, 4, result.Snapshots)
	require.EqualValues(t, 2*len(content)+singleFileSize, result.LogicalBytes)
	require.NotNil(t, result.PackedBytes)
	require.NotNil(t, result.SavedBytes)
	require.Positive(t, *result.PackedBytes)
	require.Positive(t, *result.SavedBytes)
	require.Equal(t, result.LogicalBytes-*result.PackedBytes, *result.SavedBytes)

	before, err := snapshot.ListSnapshotManifests(ctx, env.Repository, nil, nil)
	require.NoError(t, err)
	require.NoError(t, client.Get(ctx, "repo/stats", nil, &result))
	after, err := snapshot.ListSnapshotManifests(ctx, env.Repository, nil, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after)
}
