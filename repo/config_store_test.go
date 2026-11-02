package repo_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/webdav"

	"github.com/kopia/kopia/internal/testlogging"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob/throttling"
	provider "github.com/kopia/kopia/repo/blob/webdav"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/format"
)

func TestManagedConnectionLifecycle(t *testing.T) {
	ctx := testlogging.Context(t)
	root := t.TempDir()
	const providerPassword = "provider-secret-managed-fixture"
	const password = " exact repository password 雪 "
	dav := &webdav.Handler{FileSystem: webdav.Dir(t.TempDir()), LockSystem: webdav.NewMemLS()}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "managed-user" || p != providerPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="DiskPond fixture"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		dav.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	st, err := provider.New(ctx, &provider.Options{URL: httpServer.URL, Username: "managed-user", Password: providerPassword}, true)
	require.NoError(t, err)
	defer st.Close(ctx)
	require.NoError(t, repo.Initialize(ctx, st, nil, password))
	filename := filepath.Join(root, "vault.config")
	store := repo.NewManagedConfigStore(filename)
	options := &repo.ConnectOptions{ConfigStore: store, ClientOptions: repo.ClientOptions{Username: "original-user", Hostname: "original-host", Description: "Fixture", EnableActions: true, FormatBlobCacheDuration: -1}, CachingOptions: content.CachingOptions{CacheDirectory: filepath.Join(root, "cache"), ContentCacheSizeBytes: 1234567}}
	require.ErrorIs(t, repo.Connect(ctx, filename, st, "wrong", options), format.ErrInvalidPassword)
	require.NoFileExists(t, filename)
	require.NoError(t, repo.Connect(ctx, filename, st, password, options))
	assertNoManagedSecrets(t, root, password, providerPassword)

	r, err := repo.Open(ctx, filename, password, &repo.Options{ConfigStore: store})
	require.NoError(t, err)
	limits := throttling.Limits{UploadBytesPerSecond: 123456, DownloadBytesPerSecond: 456789}
	require.NoError(t, r.(repo.DirectRepository).Throttler().SetLimits(limits))
	client := r.ClientOptions()
	client.Description = "Renamed fixture"
	require.NoError(t, repo.SetClientOptions(ctx, filename, client, store))
	require.NoError(t, r.Close(ctx))
	caching, err := repo.GetCachingOptions(ctx, filename, store)
	require.NoError(t, err)
	caching.MetadataCacheSizeBytes = 987654
	require.NoError(t, repo.SetCachingOptions(ctx, filename, caching, store))
	before, err := os.ReadFile(filename)
	require.NoError(t, err)

	// A fresh process has metadata, but can neither load provider secrets nor open.
	restarted := repo.NewManagedConfigStore(filename)
	_, err = repo.LoadConfigFromFile(filename)
	require.ErrorIs(t, err, repo.ErrManagedConnection)
	_, err = repo.Open(ctx, filename, password, &repo.Options{ConfigStore: restarted})
	require.ErrorIs(t, err, repo.ErrManagedConnection)
	require.ErrorIs(t, repo.Connect(ctx, filename, st, "wrong", &repo.ConnectOptions{ConfigStore: restarted}), format.ErrInvalidPassword)
	after, err := os.ReadFile(filename)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, repo.Connect(ctx, filename, st, password, &repo.ConnectOptions{ConfigStore: restarted}))
	r, err = repo.Open(ctx, filename, password, &repo.Options{ConfigStore: restarted})
	require.NoError(t, err)
	require.Equal(t, client.Username, r.ClientOptions().Username)
	require.Equal(t, client.Hostname, r.ClientOptions().Hostname)
	require.Equal(t, client.Description, r.ClientOptions().Description)
	require.True(t, r.ClientOptions().EnableActions)
	require.Equal(t, limits, r.(repo.DirectRepository).Throttler().Limits())
	require.NoError(t, r.Close(ctx))
	actualCaching, err := repo.GetCachingOptions(ctx, filename, restarted)
	require.NoError(t, err)
	require.Equal(t, caching.CacheDirectory, actualCaching.CacheDirectory)
	require.Equal(t, caching.MetadataCacheSizeBytes, actualCaching.MetadataCacheSizeBytes)
	assertNoManagedSecrets(t, root, password, providerPassword)

	// An error after saving preserves prior metadata/cache rather than disconnecting.
	previous, err := restarted.Load()
	require.NoError(t, err)
	previous.PermissiveCacheLoading = true
	require.NoError(t, restarted.Save(previous))
	t.Setenv("KOPIA_UPGRADE_LOCK_ENABLED", "1")
	err = repo.Connect(ctx, filename, st, password, &repo.ConnectOptions{ConfigStore: restarted})
	require.ErrorIs(t, err, repo.ErrCannotWriteToRepoConnectionWithPermissiveCacheLoading)
	lc, err := restarted.Load()
	require.NoError(t, err)
	require.Equal(t, previous.ClientOptions, lc.ClientOptions)
	require.DirExists(t, caching.CacheDirectory)
	require.NoError(t, repo.Disconnect(ctx, filename, restarted))
	require.NoFileExists(t, filename)
}

func assertNoManagedSecrets(t *testing.T, root string, secrets ...string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, secret := range secrets {
			require.NotContains(t, string(data), secret, path)
			require.NotContains(t, string(data), base64.StdEncoding.EncodeToString([]byte(secret)), path)
		}
		if filepath.Ext(path) == ".config" {
			var raw map[string]any
			require.NoError(t, json.Unmarshal(data, &raw))
			require.Equal(t, true, raw["managedStorage"])
			require.NotContains(t, raw, "storage")
		}
		return nil
	}))
}
