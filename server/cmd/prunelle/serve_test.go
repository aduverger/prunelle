package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aduverger/prunelle/server/internal/config"
	"github.com/aduverger/prunelle/server/internal/fixture"
)

// The access model trusts loopback callers, so the server must refuse any other listen address.
func TestRequireLoopback(t *testing.T) {
	for listen, ok := range map[string]bool{
		"127.0.0.1:7390": true,
		"[::1]:7390":     true,
		"localhost:7390": true,
		":7390":          false,
		"0.0.0.0:7390":   false,
		"100.64.0.1:80":  false,
		"127.0.0.1":      false,
	} {
		if err := requireLoopback(listen); (err == nil) != ok {
			t.Errorf("requireLoopback(%q) = %v, want accepted=%v", listen, err, ok)
		}
	}
}

// Stopping the server lets a change already in flight complete, and does not wait for event streams.
func TestShutdownFinishesInFlightChangesAndEndsEventStreams(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repos, err := fixture.BuildRepos(filepath.Join(root, "repos"))
	if err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()
	cfg := config.Config{Listen: addr, DataDir: filepath.Join(root, "data"), WorkspaceRootPaths: []string{filepath.Join(root, "repos")}}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	served := make(chan error, 1)
	go func() { served <- serve(ctx, cfg, nil) }()

	var stream *http.Response
	for deadline := time.Now().Add(5 * time.Second); ; {
		if stream, err = http.Get("http://" + addr + "/v1/events"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer stream.Body.Close()

	// Start creating a workspace, but hold back the end of the body until the server is stopping.
	body, err := json.Marshal(fixture.WorkspaceRequest(repos))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /v1/workspaces HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", addr, len(body), body[:10])
	time.Sleep(100 * time.Millisecond)
	stop()
	time.Sleep(100 * time.Millisecond)
	conn.Write(body[10:])
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("change in flight at shutdown: status %d", resp.StatusCode)
	}

	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server still waiting after 5s; an event stream holds shutdown")
	}
	rest := new(strings.Builder)
	bufio.NewReader(stream.Body).WriteTo(rest)
	if !strings.Contains(rest.String(), "event: ready") {
		t.Errorf("stream = %q", rest.String())
	}
}
