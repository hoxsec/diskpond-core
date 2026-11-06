package server

import (
	"context"
	"time"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/snapshot"
)

type repositoryStats struct {
	ProtectedBytes int64  `json:"protectedBytes"`
	ProtectedFiles int64  `json:"protectedFiles"`
	Snapshots      int    `json:"snapshots"`
	LogicalBytes   int64  `json:"logicalBytes"`
	PackedBytes    *int64 `json:"packedBytes"`
	SavedBytes     *int64 `json:"savedBytes"`
}

// handleRepoStats reads manifests and the content index, without walking source
// files or downloading snapshot contents. Savings compare separate full copies
// of retained snapshots with indexed packed contents (including metadata). This
// is an estimate, not the provider's billed usage or available disk space.
func handleRepoStats(ctx context.Context, rc requestContext) (any, *apiError) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(rc.req.Context(), cancel)
	defer stop()

	ids, err := snapshot.ListSnapshotManifests(ctx, rc.rep, nil, nil)
	if err != nil {
		return nil, internalServerError(err)
	}
	manifests, err := snapshot.LoadSnapshots(ctx, rc.rep, ids)
	if err != nil {
		return nil, internalServerError(err)
	}

	result := summarizeRepositorySnapshots(manifests)
	direct, ok := rc.rep.(repo.DirectRepository)
	if !ok {
		return result, nil
	}

	var packed int64
	if err := direct.ContentReader().IterateContents(ctx, content.IterateOptions{}, func(info content.Info) error {
		packed += int64(info.PackedLength)
		return ctx.Err()
	}); err != nil {
		// Protection and snapshot counts remain useful if storage accounting is
		// unavailable. Never substitute a zero that would inflate the savings.
		return result, nil
	}

	saved := max(int64(0), result.LogicalBytes-packed)
	result.PackedBytes = &packed
	result.SavedBytes = &saved
	return result, nil
}

func summarizeRepositorySnapshots(manifests []*snapshot.Manifest) *repositoryStats {
	result := &repositoryStats{Snapshots: len(manifests)}
	latest := map[snapshot.SourceInfo]*snapshot.Manifest{}
	for _, manifest := range manifests {
		size, _ := snapshotProtection(manifest)
		result.LogicalBytes += size
		if manifest.IncompleteReason != "" || manifest.RootEntry == nil {
			continue
		}
		previous := latest[manifest.Source]
		if previous == nil || manifest.StartTime.After(previous.StartTime) {
			latest[manifest.Source] = manifest
		}
	}
	for _, manifest := range latest {
		size, files := snapshotProtection(manifest)
		result.ProtectedBytes += size
		result.ProtectedFiles += files
	}
	return result
}

func snapshotProtection(manifest *snapshot.Manifest) (int64, int64) {
	entry := manifest.RootEntry
	if entry == nil {
		return 0, 0
	}
	if entry.DirSummary != nil {
		return entry.DirSummary.TotalFileSize, int64(entry.DirSummary.TotalFileCount + entry.DirSummary.TotalSymlinkCount)
	}
	if entry.Type == snapshot.EntryTypeFile || entry.Type == snapshot.EntryTypeSymlink {
		return entry.FileSize, 1
	}
	return 0, 0
}
