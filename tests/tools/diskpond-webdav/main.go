// Disposable, loopback-only credentialed storage for DiskPond native smoke tests.
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/net/webdav"
)

func main() {
	root := os.Getenv("DISKPOND_WEBDAV_ROOT")
	if root == "" {
		panic("fixture root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		panic(err)
	}
	dav := &webdav.Handler{FileSystem: webdav.Dir(root), LockSystem: webdav.NewMemLS()}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Println("http://" + listener.Addr().String())
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		parts := strings.Split(strings.Trim(path.Clean(r.URL.Path), "/"), "/")
		if len(parts) < 2 || (parts[1] != "repo-0" && parts[1] != "repo-1") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		index := strings.TrimPrefix(parts[1], "repo-")
		if !ok || u != "fixture-provider-"+index || p != os.Getenv("DISKPOND_PROVIDER_SECRET_"+index) {
			w.Header().Set("WWW-Authenticate", `Basic realm="DiskPond fixture"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		dav.ServeHTTP(w, r)
	})}
	if err := server.Serve(listener); err != nil {
		panic(err)
	}
}
