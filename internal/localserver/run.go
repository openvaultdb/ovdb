package localserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/redact"
	"github.com/openvaultdb/ovdb/internal/runtime"
)

// RunOptions configure the server process.
type RunOptions struct {
	Dirs    paths.Dirs
	Port    int
	Version string
	Listen  runtime.ListenFunc // net.Listen when nil
	Log     io.Writer          // server.log in a detached server
	// FailBeforeReady makes the process exit after taking the lock and
	// before it is ready; tests use it to prove server_start_failed.
	FailBeforeReady bool
}

// Run is the local server process: it takes the home lock, binds loopback,
// publishes server.json once it serves, and runs until ctx is cancelled or
// an authenticated shutdown arrives. It removes its runtime files on exit.
func Run(ctx context.Context, opts RunOptions) error {
	logf := func(format string, args ...any) {
		line := redact.String(fmt.Sprintf(format, args...))
		_, _ = fmt.Fprintf(opts.Log, "%s %s\n", time.Now().UTC().Format(time.RFC3339), line)
	}
	fail := func(err error) error {
		logf("%s", err.Error())
		return err
	}

	instance, warnings, err := runtime.Acquire(opts.Dirs, opts.Version)
	for _, warning := range warnings {
		logf("%s", warning)
	}
	if err != nil {
		return fail(err)
	}
	defer instance.Release()
	if opts.FailBeforeReady {
		return fail(errors.New("test fault: exiting before readiness"))
	}

	listeners, listenErr := runtime.Listen(opts.Port, opts.Listen)
	if listenErr != nil {
		return fail(listenErr)
	}

	record, err := instance.NewRecord(opts.Port, time.Now())
	if err != nil {
		closeAll(listeners)
		return fail(err)
	}
	shutdown := make(chan struct{})
	var once sync.Once
	handler, err := New(Options{
		Dirs: opts.Dirs, Record: record, Secret: instance.Secret,
		RequestShutdown: func() { once.Do(func() { close(shutdown) }) },
	})
	if err != nil {
		closeAll(listeners)
		return fail(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func(listener net.Listener) { serveErr <- server.Serve(listener) }(listener)
	}
	if err := instance.Publish(record); err != nil {
		_ = server.Close()
		return fail(err)
	}
	for _, listener := range listeners {
		logf("OVDB server %s listening on %s", opts.Version, "http://"+listener.Addr().String())
	}

	var result error
	select {
	case <-ctx.Done():
		logf("stopping: %v", ctx.Err())
	case <-shutdown:
		logf("stopping: shutdown requested")
	case err := <-serveErr:
		result = fail(err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logf("shutdown: %v", err)
	}
	logf("stopped")
	return result
}

func closeAll(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}
