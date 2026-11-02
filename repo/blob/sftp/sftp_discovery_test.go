package sftp_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/kopia/kopia/internal/gather"
	"github.com/kopia/kopia/internal/testlogging"
	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/sftp"
)

// Exercise the real SSH/SFTP client against an isolated in-memory server, with a
// pinned host key. This requires neither Docker nor a developer's SFTP account.
func TestSFTPDiscoveryDoesNotCreateDestination(t *testing.T) {
	t.Parallel()
	for _, isCreate := range []bool{false, true} {
		t.Run(fmt.Sprintf("create-%v", isCreate), func(t *testing.T) {
			ctx := testlogging.Context(t)
			handlers := pkgsftp.InMemHandler()
			writes := &countSFTPWrites{FileCmder: handlers.FileCmd, FileWriter: handlers.FilePut}
			handlers.FileCmd = writes
			handlers.FilePut = writes
			opt := startDiscoverySFTPServer(t, handlers)
			opt.Path = "/missing/repository"

			st, err := sftp.New(ctx, &opt, isCreate)
			require.NoError(t, err)
			defer st.Close(ctx)

			var data gather.WriteBuffer
			defer data.Close()
			require.ErrorIs(t, st.GetBlob(ctx, "kopia.repository", 0, -1, &data), blob.ErrBlobNotFound)
			_, err = st.GetMetadata(ctx, "kopia.repository")
			require.ErrorIs(t, err, blob.ErrBlobNotFound)
			if !isCreate {
				require.Zero(t, writes.count.Load(), "discovery sent a mutating SFTP request")
				return
			}

			require.Positive(t, writes.count.Load(), "explicit creation did not create the path")
			require.NoError(t, st.PutBlob(ctx, "kopia.repository", gather.FromSlice([]byte("fixture")), blob.PutOptions{}))
			require.NoError(t, st.GetBlob(ctx, "kopia.repository", 0, -1, &data))
			require.Equal(t, "fixture", string(data.ToByteSlice()))
		})
	}
}

type countSFTPWrites struct {
	pkgsftp.FileCmder
	pkgsftp.FileWriter
	count atomic.Int32
}

func (c *countSFTPWrites) Filecmd(r *pkgsftp.Request) error {
	c.count.Add(1)
	return c.FileCmder.Filecmd(r)
}

func (c *countSFTPWrites) Filewrite(r *pkgsftp.Request) (io.WriterAt, error) {
	c.count.Add(1)
	return c.FileWriter.Filewrite(r)
}

func startDiscoverySFTPServer(t *testing.T, handlers pkgsftp.Handlers) sftp.Options {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	config := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if string(password) != "discovery-fixture-password" {
			return nil, fmt.Errorf("invalid fixture password")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	t.Cleanup(func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("fixture SFTP connection did not close")
		}
	})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		server, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			if incoming.ChannelType() != "session" {
				incoming.Reject(ssh.UnknownChannelType, "session required")
				continue
			}
			channel, requests, err := incoming.Accept()
			if err != nil {
				return
			}
			go func() {
				for request := range requests {
					var payload struct{ Subsystem string }
					err := ssh.Unmarshal(request.Payload, &payload)
					request.Reply(request.Type == "subsystem" && err == nil && payload.Subsystem == "sftp", nil)
				}
			}()
			s := pkgsftp.NewRequestServer(channel, handlers)
			s.Serve()
			s.Close()
		}
	}()
	address := listener.Addr().(*net.TCPAddr)
	return sftp.Options{
		Host: "127.0.0.1", Port: address.Port, Username: "fixture", Password: "discovery-fixture-password",
		KnownHostsData: knownhosts.Line([]string{listener.Addr().String()}, signer.PublicKey()),
	}
}
