package repo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/kopia/kopia/repo/blob"
)

// NewManagedStorage prevents provider errors from persisting credentials in
// snapshot outcomes or surfacing them through task APIs. The desktop handles
// console redaction; managed sidecars disable unsanitized file logging.
func NewManagedStorage(ctx context.Context, ci blob.ConnectionInfo, create bool) (blob.Storage, error) {
	redact := connectionRedactor(ci)
	st, err := blob.NewStorage(ctx, ci, create)
	if err != nil {
		return nil, redact(err)
	}
	return &managedStorage{Storage: st, redact: redact}, nil
}

type managedStorage struct {
	blob.Storage
	redact func(error) error
}

func (s *managedStorage) DisplayName() string { return "DiskPond storage" }
func (s *managedStorage) GetBlob(ctx context.Context, id blob.ID, offset, length int64, output blob.OutputBuffer) error {
	return s.redact(s.Storage.GetBlob(ctx, id, offset, length, output))
}
func (s *managedStorage) GetMetadata(ctx context.Context, id blob.ID) (blob.Metadata, error) {
	m, err := s.Storage.GetMetadata(ctx, id)
	return m, s.redact(err)
}
func (s *managedStorage) ListBlobs(ctx context.Context, prefix blob.ID, cb func(blob.Metadata) error) error {
	return s.redact(s.Storage.ListBlobs(ctx, prefix, cb))
}
func (s *managedStorage) PutBlob(ctx context.Context, id blob.ID, data blob.Bytes, opts blob.PutOptions) error {
	return s.redact(s.Storage.PutBlob(ctx, id, data, opts))
}
func (s *managedStorage) DeleteBlob(ctx context.Context, id blob.ID) error {
	return s.redact(s.Storage.DeleteBlob(ctx, id))
}
func (s *managedStorage) Close(ctx context.Context) error { return s.redact(s.Storage.Close(ctx)) }
func (s *managedStorage) FlushCaches(ctx context.Context) error {
	return s.redact(s.Storage.FlushCaches(ctx))
}
func (s *managedStorage) ExtendBlobRetention(ctx context.Context, id blob.ID, opts blob.ExtendOptions) error {
	return s.redact(s.Storage.ExtendBlobRetention(ctx, id, opts))
}
func (s *managedStorage) GetCapacity(ctx context.Context) (blob.Capacity, error) {
	c, err := s.Storage.GetCapacity(ctx)
	return c, s.redact(err)
}

type redactedConnectionError struct {
	cause error
	text  string
}

func (e redactedConnectionError) Error() string              { return e.text }
func (e redactedConnectionError) Unwrap() error              { return e.cause }
func (e redactedConnectionError) Format(s fmt.State, _ rune) { fmt.Fprint(s, e.text) }

func connectionRedactor(ci blob.ConnectionInfo) func(error) error {
	values := map[string]bool{}
	var collect func(any)
	collect = func(v any) {
		switch x := v.(type) {
		case string:
			if x == "" {
				return
			}
			values[x] = true
			values[base64.StdEncoding.EncodeToString([]byte(x))] = true
			values[url.QueryEscape(x)] = true
			if u, err := url.Parse(x); err == nil && u.Host != "" {
				if u.User != nil {
					collect(u.User.Username())
					p, _ := u.User.Password()
					collect(p)
				}
				for _, vs := range u.Query() {
					for _, s := range vs {
						collect(s)
					}
				}
			}
			if strings.HasPrefix(x, "{") {
				var j any
				if json.Unmarshal([]byte(x), &j) == nil {
					collect(j)
				}
			}
		case map[string]any:
			for _, v := range x {
				collect(v)
			}
		case []any:
			for _, v := range x {
				collect(v)
			}
		}
	}
	b, _ := json.Marshal(ci.Config)
	var parsed any
	if json.Unmarshal(b, &parsed) == nil {
		collect(parsed)
	}
	var sorted []string
	for v := range values {
		sorted = append(sorted, v)
	}
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	var pairs []string
	for _, v := range sorted {
		pairs = append(pairs, v, "[redacted]")
	}
	r := strings.NewReplacer(pairs...)
	return func(err error) error {
		if err == nil {
			return nil
		}
		return redactedConnectionError{err, r.Replace(err.Error())}
	}
}
