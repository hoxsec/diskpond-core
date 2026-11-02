package repo

import (
	"os"
	"sync"

	"github.com/pkg/errors"

	"github.com/kopia/kopia/repo/blob"
)

// ConfigStore owns one repository's local connection configuration.
// Managed desktop connections keep provider data in memory, while retaining
// client/cache metadata at the same filename as ordinary connections.
type ConfigStore interface {
	Load() (*LocalConfig, error)
	Save(*LocalConfig) error
	Remove() error
}

type fileConfigStore struct{ filename string }

func (s fileConfigStore) Load() (*LocalConfig, error) { return LoadConfigFromFile(s.filename) }
func (s fileConfigStore) Save(lc *LocalConfig) error  { return lc.writeToFile(s.filename) }
func (s fileConfigStore) Remove() error               { return os.Remove(s.filename) }

func connectionConfigStore(filename string, stores ...ConfigStore) ConfigStore {
	if len(stores) > 0 && stores[0] != nil {
		return stores[0]
	}
	return fileConfigStore{filename}
}

// ErrManagedConnection indicates that provider credentials must be supplied by
// the desktop application. Standalone clients must not reopen managed files.
var ErrManagedConnection = errors.New("provider connection must be supplied by DiskPond")

type managedConfigStore struct {
	filename string
	mu       sync.Mutex
	storage  *blob.ConnectionInfo
}

// NewManagedConfigStore creates an isolated, process-local provider store.
// Existing plaintext connections must first be migrated by the desktop owner.
func NewManagedConfigStore(filename string) ConfigStore {
	return &managedConfigStore{filename: filename}
}

func (s *managedConfigStore) Load() (*LocalConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lc, err := loadConfigFromFile(s.filename)
	if err != nil {
		return nil, err
	}
	if !lc.ManagedStorage || lc.Storage != nil || lc.APIServer != nil {
		return nil, errors.New("legacy provider connection requires desktop migration")
	}
	lc.Storage = s.storage
	return lc, nil
}

func (s *managedConfigStore) Save(lc *LocalConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lc.APIServer != nil {
		return errors.New("API-server connections are unsupported in managed mode")
	}
	safe := *lc
	safe.ManagedStorage = true
	safe.Storage = nil
	if err := safe.writeToFile(s.filename); err != nil {
		return err
	}
	s.storage = lc.Storage
	return nil
}

func (s *managedConfigStore) Remove() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.filename); err != nil {
		return err
	}
	s.storage = nil
	return nil
}
