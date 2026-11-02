package repo

import (
	"context"
	"os"

	"github.com/pkg/errors"

	"github.com/kopia/kopia/internal/atomicfile"
	"github.com/kopia/kopia/internal/ospath"
	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/format"
)

// ConnectOptions specifies options when persisting configuration to connect to a repository.
type ConnectOptions struct {
	ClientOptions
	ConfigStore ConfigStore `json:"-"`

	content.CachingOptions
}

// ErrRepositoryNotInitialized is returned when attempting to connect to repository that has not
// been initialized.
var ErrRepositoryNotInitialized = errors.New("repository not initialized in the provided storage")

// Connect connects to the repository in the specified storage and persists the configuration and credentials in the file provided.
func Connect(ctx context.Context, configFile string, st blob.Storage, password string, opt *ConnectOptions) error {
	if opt == nil {
		opt = &ConnectOptions{}
	}

	f, err := format.ReadRepositoryBlob(ctx, st)
	if err != nil {
		if errors.Is(err, blob.ErrBlobNotFound) {
			return ErrRepositoryNotInitialized
		}
		return errors.Wrap(err, "unable to read format blob")
	}
	// Authenticate before either legacy migration or writing local configuration.
	if err := f.ValidatePassword(password); err != nil {
		return err
	}
	var lc LocalConfig
	var previous *LocalConfig
	store := connectionConfigStore(configFile, opt.ConfigStore)
	if opt.ConfigStore != nil {
		previous, err = store.Load()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if previous != nil {
			lc = *previous
		}
	}
	format.MigrateLegacyBlobIDs(ctx, st)

	ci := st.ConnectionInfo()
	lc.Storage = &ci
	if opt.ConfigStore == nil {
		lc.ClientOptions = opt.ApplyDefaults(ctx, "Repository in "+st.DisplayName())
	} else if previous == nil {
		lc.ClientOptions = opt.ClientOptions.ApplyDefaults(ctx, "DiskPond vault")
	} else {
		lc.ClientOptions = lc.ClientOptions.Override(opt.ClientOptions).ApplyDefaults(ctx, "DiskPond vault")
	}

	if lc.Caching == nil {
		if err = setupCachingOptionsWithDefaults(ctx, configFile, &lc, &opt.CachingOptions, f.UniqueID); err != nil {
			return errors.Wrap(err, "unable to set up caching")
		}
	}

	if err := store.Save(&lc); err != nil {
		return errors.Wrap(err, "unable to write config file")
	}

	if opt.ConfigStore == nil {
		return verifyConnect(ctx, configFile, password)
	}

	// A failed managed reconnect must not delete the prior identity, cache or
	// bandwidth settings. Only metadata was written; provider data stays in RAM.
	r, err := Open(ctx, configFile, password, &Options{ConfigStore: store})
	if err == nil {
		return errors.Wrap(r.Close(ctx), "error closing repository")
	}
	var rollbackErr error
	if previous != nil {
		rollbackErr = store.Save(previous)
	} else {
		rollbackErr = store.Remove()
	}
	if rollbackErr != nil {
		return errors.Wrap(err, "connection failed and local metadata could not be restored")
	}
	return err
}

func verifyConnect(ctx context.Context, configFile, password string, stores ...ConfigStore) error {
	store := connectionConfigStore(configFile, stores...)
	// now verify that the repository can be opened with the provided config file.
	r, err := Open(ctx, configFile, password, &Options{ConfigStore: store})
	if err != nil {
		// we failed to open the repository after writing the config file,
		// remove the config file we just wrote and any caches.
		if derr := Disconnect(ctx, configFile, store); derr != nil {
			log(ctx).Errorf("unable to disconnect after unsuccessful opening: %v", derr)
		}

		return err
	}

	return errors.Wrap(r.Close(ctx), "error closing repository")
}

// Disconnect removes the specified configuration file and any local cache directories.
func Disconnect(ctx context.Context, configFile string, stores ...ConfigStore) error {
	store := connectionConfigStore(configFile, stores...)
	cfg, err := store.Load()
	if err != nil {
		return err
	}

	if cfg.Caching != nil && cfg.Caching.CacheDirectory != "" {
		if !ospath.IsAbs(cfg.Caching.CacheDirectory) {
			return errors.New("cache directory was not absolute, refusing to delete")
		}

		if err = os.RemoveAll(cfg.Caching.CacheDirectory); err != nil {
			log(ctx).Errorf("unable to remove cache directory: %v", err)
		}
	}

	if err := os.RemoveAll(atomicfile.MaybePrefixLongFilenameOnWindows(configFile + ".backup-outcomes")); err != nil {
		log(ctx).Errorf("unable to remove backup outcomes: %v", err)
	}

	maintenanceLock := configFile + ".mlock"
	if err := os.RemoveAll(maintenanceLock); err != nil {
		log(ctx).Error("unable to remove maintenance lock file", maintenanceLock)
	}

	//nolint:wrapcheck
	return store.Remove()
}

// SetClientOptions updates client options stored in the provided configuration file.
func SetClientOptions(_ context.Context, configFile string, cliOpt ClientOptions, stores ...ConfigStore) error {
	store := connectionConfigStore(configFile, stores...)
	lc, err := store.Load()
	if err != nil {
		return err
	}

	lc.ClientOptions = cliOpt

	return store.Save(lc)
}
