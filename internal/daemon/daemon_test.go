package daemon_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
