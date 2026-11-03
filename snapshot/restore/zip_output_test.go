package restore

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kopia/kopia/fs/localfs"
)

type failingZipDestination struct {
	bytes.Buffer
	writeErr error
	closeErr error
	closes   int
}

func (w *failingZipDestination) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}

	return w.Buffer.Write(p)
}

func (w *failingZipDestination) Close() error {
	w.closes++
	return w.closeErr
}

func TestZipOutputClose(t *testing.T) {
	writeErr := errors.New("destination full")
	closeErr := errors.New("destination close failed")

	for _, tc := range []struct {
		name     string
		writeErr error
		closeErr error
		wantErr  error
	}{
		{"success", nil, nil, nil},
		{"finalization failure", writeErr, nil, writeErr},
		{"destination close failure", nil, closeErr, closeErr},
		{"both failures", writeErr, closeErr, writeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := &failingZipDestination{writeErr: tc.writeErr, closeErr: tc.closeErr}
			output := NewZipOutput(destination, zip.Store)
			if err := output.Close(context.Background()); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Close() = %v, want %v", err, tc.wantErr)
			}
			if destination.closes != 1 {
				t.Fatalf("underlying destination closed %v times, want once", destination.closes)
			}
		})
	}
}

func TestZipOutputCloseAfterCopyFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("fixture"), 16384), 0o600); err != nil {
		t.Fatal(err)
	}
	entry, err := localfs.NewEntry(path)
	if err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("destination full during copy")
	destination := &failingZipDestination{writeErr: writeErr, closeErr: errors.New("close failed")}
	_, err = Entry(context.Background(), nil, NewZipOutput(destination, zip.Store), entry, Options{})
	if !errors.Is(err, writeErr) {
		t.Fatalf("Entry() = %v, want copy error %v", err, writeErr)
	}
	if destination.closes != 1 {
		t.Fatalf("underlying destination closed %v times, want once", destination.closes)
	}
}
