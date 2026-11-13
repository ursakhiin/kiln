package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	kilnv1 "github.com/ursakhiin/kiln/gen/go/kiln/v1"
	"github.com/ursakhiin/kiln/gen/go/kiln/v1/kilnv1connect"
	"github.com/ursakhiin/kiln/internal/daemon"
)

func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := daemon.FindRepoRoot(nested)
	if err != nil || got != root {
		t.Fatalf("FindRepoRoot = %q, %v; want %q", got, err, root)
	}

	bare := t.TempDir()
	for d := bare; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			t.Skip("an ancestor of the temp dir is a git repo")
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	if _, err := daemon.FindRepoRoot(bare); err == nil {
		t.Fatal("expected an error outside a git repo")
	}
}

func TestRun(t *testing.T) {
	repo := t.TempDir()
	cfg := daemon.Config{
		RepoRoot: repo,
		Addr:     "127.0.0.1:0",
		Version:  "test",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, cfg, func(a string) { addrCh <- a }) }()
	var addr string
	select {
	case addr = <-addrCh:
	case err := <-done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for ready")
	}

	runFile := filepath.Join(repo, ".kiln", "run", "daemon.json")
	data, err := os.ReadFile(runFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"addr":"` + addr + `"`; !strings.Contains(string(data), want) {
		t.Errorf("daemon.json = %s; want it to contain %s", data, want)
	}
	gi, err := os.ReadFile(filepath.Join(repo, ".kiln", ".gitignore"))
	if err != nil || string(gi) != "run/\n" {
		t.Errorf(".gitignore = %q, %v", gi, err)
	}

	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: "http://" + addr + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 15 {
		t.Errorf("got %d tools; want 15", len(tools.Tools))
	}
	_ = sess.Close()

	api := kilnv1connect.NewKilnServiceClient(http.DefaultClient, "http://"+addr)
	_, err = api.CreateItem(ctx, connect.NewRequest(&kilnv1.CreateItemRequest{Item: &kilnv1.Item{
		Title: "P",
		Kind:  &kilnv1.Item_Project{Project: &kilnv1.Project{}},
	}}))
	if err != nil {
		t.Errorf("CreateItem: %v", err)
	}

	if err := daemon.Run(ctx, cfg, nil); !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Errorf("second Run = %v; want ErrAlreadyRunning", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for shutdown")
	}
	if _, err := os.Stat(runFile); !os.IsNotExist(err) {
		t.Errorf("daemon.json still exists: %v", err)
	}
}

// startDaemon runs a daemon on addr in a fresh repo until the test ends and
// returns its bound address.
func startDaemon(t *testing.T, addr string, logger *slog.Logger) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := daemon.Config{RepoRoot: t.TempDir(), Addr: addr, Version: "test", Logger: logger}
	addrCh := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, cfg, func(a string) { addrCh <- a }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("timed out waiting for shutdown")
		}
	})
	select {
	case a := <-addrCh:
		return a
	case err := <-done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for ready")
	}
	return ""
}

// A DNS-rebound page reaches the loopback daemon under its own host name; the
// daemon must refuse it on every endpoint, Connect included.
func TestRejectsNonLocalHost(t *testing.T) {
	addr := startDaemon(t, "127.0.0.1:0", slog.New(slog.DiscardHandler))
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	// No keep-alives: an open connection would hold up the daemon's shutdown.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	for _, tc := range []struct {
		host string
		want int
	}{
		{"evil.example", http.StatusForbidden},
		{"evil.example:" + port, http.StatusForbidden},
		{"127.0.0.1:" + port, http.StatusOK},
		{"localhost:" + port, http.StatusOK},
		{"localhost", http.StatusOK},
		{"[::1]:" + port, http.StatusOK},
	} {
		req, err := http.NewRequest(http.MethodPost, "http://"+addr+kilnv1connect.KilnServiceListItemsProcedure, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = tc.host
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("Host %q: status %d, want %d", tc.host, resp.StatusCode, tc.want)
		}
	}
}

func TestWarnsOnNonLoopbackAddr(t *testing.T) {
	var buf syncBuffer
	startDaemon(t, "0.0.0.0:0", slog.New(slog.NewTextHandler(&buf, nil)))
	if got := buf.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "no authentication") {
		t.Fatalf("log has no warning about the exposed API:\n%s", got)
	}
}

func TestNoWarningOnLoopbackAddr(t *testing.T) {
	var buf syncBuffer
	startDaemon(t, "127.0.0.1:0", slog.New(slog.NewTextHandler(&buf, nil)))
	if got := buf.String(); strings.Contains(got, "level=WARN") {
		t.Fatalf("unexpected warning:\n%s", got)
	}
}

// syncBuffer is a bytes.Buffer safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
