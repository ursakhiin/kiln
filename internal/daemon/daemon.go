package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/ursakhiin/underdraw/internal/api"
	underdrawmcp "github.com/ursakhiin/underdraw/internal/mcp"
	"github.com/ursakhiin/underdraw/internal/service"
	"github.com/ursakhiin/underdraw/internal/store"
)

// DefaultAddr is where the daemon listens unless told otherwise. It is
// loopback only.
const DefaultAddr = "127.0.0.1:7477"

// ErrAlreadyRunning is returned by Run when another daemon holds the repo's lock.
var ErrAlreadyRunning = errors.New("underdraw serve is already running for this repo")

const shutdownGrace = 5 * time.Second

// Config configures Run.
type Config struct {
	RepoRoot string
	Addr     string // "" means DefaultAddr
	Version  string
	Logger   *slog.Logger // nil means slog.Default()
}

// FindRepoRoot walks up from start to the first directory containing .git
// (a directory or a file, as in worktrees).
func FindRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no git repository found at or above %s", start)
		}
		dir = parent
	}
}

// Run serves the repo until ctx is done, then shuts down (with a 5s grace
// period) and returns nil. ready, if non-nil, is called with the bound
// address once the daemon is listening.
func Run(ctx context.Context, cfg Config, ready func(addr string)) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	addr := cfg.Addr
	if addr == "" {
		addr = DefaultAddr
	}

	st, err := store.Open(cfg.RepoRoot)
	if err != nil {
		return err
	}

	underdrawDir := filepath.Join(cfg.RepoRoot, ".underdraw")
	runDir := filepath.Join(underdrawDir, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	if err := writeIfMissing(filepath.Join(underdrawDir, ".gitignore"), "run/\n"); err != nil {
		return err
	}

	lock := flock.New(filepath.Join(runDir, "daemon.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return ErrAlreadyRunning
	}
	defer func() { _ = lock.Unlock() }()

	svc, fileErrs, err := service.New(st, service.Options{Logger: logger})
	if err != nil {
		return err
	}
	for _, fe := range fileErrs {
		logger.Warn("skipped unreadable file", "error", fe)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	bound := ln.Addr().String()
	if !isLoopback(ln.Addr()) {
		logger.Warn("listening beyond loopback: the Connect API acts as the user with no authentication and is exposed beyond this machine",
			"addr", bound)
	}

	runFile := filepath.Join(runDir, "daemon.json")
	info, err := json.Marshal(struct {
		Addr string `json:"addr"`
		PID  int    `json:"pid"`
	}{bound, os.Getpid()})
	if err != nil {
		_ = ln.Close()
		return err
	}
	if err := writeAtomic(runFile, info); err != nil {
		_ = ln.Close()
		return err
	}
	defer func() { _ = os.Remove(runFile) }()

	mux := http.NewServeMux()
	mux.Handle("/mcp", underdrawmcp.NewHandler(underdrawmcp.NewServer(svc, cfg.Version)))
	apiPath, apiHandler := api.NewHandler(svc)
	mux.Handle(apiPath, apiHandler)

	srv := &http.Server{Handler: localHostOnly(mux)}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	logger.Info("underdraw serving", "repo", cfg.RepoRoot, "addr", bound)
	if ready != nil {
		ready(bound)
	}

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		// Open streams didn't finish in time; drop them.
		_ = srv.Close()
	}
	return nil
}

// isLoopback reports whether addr is a TCP address on a loopback IP.
func isLoopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// localHostOnly guards against DNS rebinding: on a connection to a loopback
// address it rejects, with 403, any request whose Host isn't a localhost name.
// A rebound page reaches the daemon under its own host name, and is
// same-origin to itself, so cross-origin protection alone doesn't stop it.
// It wraps the whole mux so every endpoint gets it.
func localHostOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && isLoopback(local) && !isLocalHost(r.Host) {
			http.Error(w, "forbidden: Host must be localhost, 127.0.0.1 or [::1]", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// isLocalHost reports whether host, a Host header with or without a port,
// names this machine's loopback interface.
func isLocalHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return false
}

func writeIfMissing(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// writeAtomic writes data to path through a temp file in the same directory.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".daemon-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
