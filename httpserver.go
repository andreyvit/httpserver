package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	LocalIP4Addr             = "127.0.0.1"
	AllNetworkInterfacesAddr = "0.0.0.0"
)

func PickBindAddr(isLocal bool) string {
	if isLocal {
		return LocalIP4Addr
	} else {
		return AllNetworkInterfacesAddr
	}
}

type Options struct {
	DebugName   string
	Addr        string
	Port        int
	AcmeEnabled bool
	AcmeDir     string
	Logf        func(format string, args ...any)

	GracefulShutdownTimeout time.Duration
}

type Server struct {
	hs http.Server

	debugName               string
	gracefulShutdownTimeout time.Duration

	shutdownRequestC chan struct{}
	closedWG         sync.WaitGroup
}

func Start(ctx context.Context, handler http.Handler, closed func(err error), opt Options) (*Server, error) {
	if opt.DebugName == "" {
		opt.DebugName = "httpserver"
	}
	if opt.GracefulShutdownTimeout == 0 {
		opt.GracefulShutdownTimeout = 10 * time.Second
	}
	if opt.Addr == "" {
		opt.Addr = LocalIP4Addr // safe default
	}

	srv := &Server{
		hs: http.Server{
			Addr:    fmt.Sprintf("%s:%d", opt.Addr, opt.Port),
			Handler: handler,
			BaseContext: func(l net.Listener) context.Context {
				return ctx
			},
		},

		debugName:               opt.DebugName,
		gracefulShutdownTimeout: opt.GracefulShutdownTimeout,

		shutdownRequestC: make(chan struct{}, 1),
	}

	l, err := net.Listen("tcp", srv.hs.Addr)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot listen on %s: %w", srv.debugName, srv.hs.Addr, err)
	}

	srv.closedWG.Add(1) // .Done() in shutdownWhenRequested

	serveC := make(chan error)
	go srv.serve(l, serveC)
	go srv.shutdownWhenRequested(ctx, serveC, closed)
	return srv, nil
}

// Shutdown initiates shutting down of the server (if it is still running) and returns without waiting.
func (srv *Server) Shutdown() {
	select {
	case srv.shutdownRequestC <- struct{}{}:
		break
	default:
		break
	}
}

// Close calls Shutdown (in case the server is still running) and waits until the shutdown process is finished.
func (srv *Server) Close() {
	srv.Shutdown()
	srv.closedWG.Wait()
}

func (srv *Server) serve(l net.Listener, serveC chan<- error) {
	err := srv.hs.Serve(l) // guarantees that l is closed
	if err == http.ErrServerClosed {
		err = nil
	} else if err != nil {
		err = fmt.Errorf("%s: Serve failed: %w", srv.debugName, err)
	}
	serveC <- err
}

func (srv *Server) shutdownWhenRequested(ctx context.Context, serveC <-chan error, closed func(err error)) {
	defer srv.closedWG.Done()

	select {
	case <-srv.shutdownRequestC:
		break
	case <-ctx.Done():
		break
	}

	var shutdownErr error
	defer func() { // Calls http.Server.Close(), recovers from shutdown panics, waits on Serve
		shutdownErr = appendErr(shutdownErr, srv.translateShutdownPanic(recover()))
		closeErr := ignoreServerClosed(srv.hs.Close())
		serveErr := <-serveC
		closed(appendErr(serveErr, appendErr(shutdownErr, closeErr)))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), srv.gracefulShutdownTimeout)
	defer cancel()
	shutdownErr = srv.translateShutdownError(srv.hs.Shutdown(ctx))
}

func (srv *Server) translateShutdownError(err error) error {
	switch err {
	case nil, http.ErrServerClosed:
		return nil
	case context.DeadlineExceeded:
		return fmt.Errorf("%s: graceful shutdown timed out after %.1f sec", srv.debugName, srv.gracefulShutdownTimeout.Seconds())
	default:
		return fmt.Errorf("%s: graceful shutdown failed: %w", srv.debugName, err)
	}
}

func (srv *Server) translateShutdownPanic(panicVal any) error {
	if panicVal == nil {
		return nil
	}
	err, ok := panicVal.(error)
	if !ok {
		err = fmt.Errorf("%v", panicVal)
	}
	return fmt.Errorf("%s: panic during shutdown: %w", srv.debugName, err)
}

func ignoreServerClosed(err error) error {
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type multi []error

func (m multi) Error() string {
	var buf strings.Builder
	for i, err := range m {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(strings.ReplaceAll(err.Error(), "\n", "\n\t"))
	}
	return buf.String()
}

func appendErr(dest error, err error) error {
	if err == nil {
		return dest
	} else if dest == nil {
		return err
	} else if destM, ok := dest.(multi); ok {
		if errM, ok := err.(multi); ok {
			return append(destM, errM...)
		} else {
			return append(destM, err)
		}
	} else {
		if errM, ok := err.(multi); ok {
			return append(multi{dest}, errM...)
		} else {
			return multi{dest, err}
		}
	}
}
