package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kopia/kopia/internal/serverapi"
	"github.com/kopia/kopia/internal/testlogging"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/filesystem"
)

func TestRepoExistsDoesNotModifyStorage(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{
		"empty", "missing", "unrelated", "current", "legacy", "both",
		"current-no-shards", "legacy-no-shards", "invalid-current", "invalid-shards", "unreadable-current",
		"null-current", "missing-identity",
	} {
		t.Run(fixture, func(t *testing.T) {
			ctx := testlogging.Context(t)
			root := t.TempDir()
			dir := filepath.Join(root, "repository")
			if fixture != "missing" {
				require.NoError(t, os.Mkdir(dir, 0o700))
			}

			const password = " discovery fixture 雪 "
			id := []byte("discovery-fixture-id")
			wantCode := serverapi.APIErrorCode("")
			switch fixture {
			case "empty", "missing", "unrelated":
				wantCode = serverapi.ErrorNotInitialized
				if fixture == "missing" {
					// The filesystem provider rejects inaccessible storage roots.
					wantCode = serverapi.ErrorInternal
				}
				if fixture == "unrelated" {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("not a repository"), 0o600))
				}
			default:
				st, err := filesystem.New(ctx, &filesystem.Options{Path: dir}, true)
				require.NoError(t, err)
				require.NoError(t, repo.Initialize(ctx, st, &repo.NewRepositoryOptions{UniqueID: id}, password))
				require.NoError(t, st.Close(ctx))

				if fixture == "legacy" || fixture == "legacy-no-shards" {
					for _, suffix := range []string{"repository", "blobcfg"} {
						require.NoError(t, os.Rename(filepath.Join(dir, "kopia."+suffix+".f"), filepath.Join(dir, "blinkdisk."+suffix+".f")))
					}
				}
				if fixture == "both" || fixture == "invalid-current" || fixture == "unreadable-current" {
					// A legacy blob must not mask a malformed or unreadable current blob.
					require.NoError(t, os.WriteFile(filepath.Join(dir, "blinkdisk.repository.f"), []byte(`{"uniqueID":"bGVnYWN5"}`), 0o600))
				}

				switch fixture {
				case "current-no-shards", "legacy-no-shards":
					require.NoError(t, os.Remove(filepath.Join(dir, ".shards")))
				case "invalid-current":
					require.NoError(t, os.WriteFile(filepath.Join(dir, "kopia.repository.f"), []byte("{invalid"), 0o600))
					wantCode = serverapi.ErrorInternal
				case "null-current", "missing-identity":
					data := []byte("null")
					if fixture == "missing-identity" {
						data = []byte("{}")
					}
					require.NoError(t, os.WriteFile(filepath.Join(dir, "kopia.repository.f"), data, 0o600))
					wantCode = serverapi.ErrorInternal
				case "invalid-shards":
					require.NoError(t, os.WriteFile(filepath.Join(dir, ".shards"), []byte("{invalid"), 0o600))
					wantCode = serverapi.ErrorInternal
				case "unreadable-current":
					require.NoError(t, os.Remove(filepath.Join(dir, "kopia.repository.f")))
					require.NoError(t, os.Mkdir(filepath.Join(dir, "kopia.repository.f"), 0o700))
					wantCode = serverapi.ErrorInternal
				}
			}

			before := discoveryTree(t, root)
			for _, enteredPassword := range []string{"", password, "incorrect"} {
				body, err := json.Marshal(serverapi.CheckRepositoryExistsRequest{
					Storage:  blob.ConnectionInfo{Type: "filesystem", Config: &filesystem.Options{Path: dir}},
					Password: enteredPassword,
				})
				require.NoError(t, err)
				response, apiErr := handleRepoExists(ctx, requestContext{body: body})
				code := wantCode
				if code == "" && enteredPassword == "incorrect" {
					code = serverapi.ErrorInvalidPassword
				}
				if code != "" {
					require.NotNil(t, apiErr)
					require.Equal(t, code, apiErr.apiErrorCode, apiErr.message)
				} else {
					require.Nil(t, apiErr)
					require.Equal(t, &serverapi.CheckRepositoryExistsResponse{UniqueID: base64.StdEncoding.EncodeToString(id)}, response)
				}
				require.Equal(t, before, discoveryTree(t, root), "discovery changed the destination")
			}
		})
	}
}

type discoveryEntry struct {
	Mode    fs.FileMode
	Size    int64
	ModTime time.Time
	Hash    [sha256.Size]byte
}

func discoveryTree(t *testing.T, root string) map[string]discoveryEntry {
	t.Helper()
	entries := map[string]discoveryEntry{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		require.NoError(t, walkErr)
		info, err := d.Info()
		require.NoError(t, err)
		entry := discoveryEntry{Mode: info.Mode()}
		if !d.IsDir() {
			// Windows may defer directory metadata updates from fixture creation.
			// Compare the complete entry set and file metadata/content instead.
			entry.Size = info.Size()
			entry.ModTime = info.ModTime()
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			entry.Hash = sha256.Sum256(data)
		}
		entries[path] = entry
		return nil
	}))
	return entries
}
