# httpserver

Run a standard Go HTTP handler with context-driven shutdown and a bounded graceful shutdown period.

## Install

Requires Go 1.20 or newer.

```sh
go get github.com/andreyvit/httpserver@latest
```

## Example

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/andreyvit/httpserver"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "hello") })
	srv, err := httpserver.Start(ctx, handler, func(err error) {
		if err != nil {
			panic(err)
		}
	}, httpserver.Options{Addr: "127.0.0.1", Port: 0, GracefulShutdownTimeout: 5 * time.Second})
	if err != nil {
		panic(err)
	}
	// Port 0 selects a free local port. Use a fixed port for a real service.
	cancel()
	srv.Close() // wait for shutdown and the callback
	fmt.Println("stopped")
}
```

`Shutdown` requests shutdown without waiting. `Close` waits. Supply a non-nil completion callback; it receives serve and shutdown errors. Context cancellation also requests shutdown.

## License

See [LICENSE](LICENSE).
