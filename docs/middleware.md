# HTTP Middleware

How the middleware in `internal/middleware/middleware.go` is chained together, how a request flows through it, and how `Logging` captures the response status code.

## The middleware shape

Every middleware has the same type:

```go
func(http.Handler) http.Handler
```

It takes a handler (`next`) and returns a new handler that does extra work before and/or after calling `next.ServeHTTP`. `Recovery` and `Logging` take a logger first, so they are functions that *return* a middleware: `Logging(logger)` has the type above.

## Chain: building the stack

```go
func Chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
```

`Chain` wraps a handler in every middleware and returns a single handler. `internal/server/server.go` uses it like this:

```go
srv.handler = middleware.Chain(
	srv.mux,
	middleware.Recovery(srv.logger),
	middleware.RequestID,
	middleware.Logging(srv.logger),
)
```

### The loop line

```go
handler = middlewares[i](handler)
```

Read it inside out:

1. `middlewares[i]` takes item `i` from the slice. Each item is a function, so it can be called.
2. `(handler)` calls that function with the current `handler`. The middleware returns a new handler that wraps the old one.
3. `handler = ...` stores the result back in the same variable.

It is the same as:

```go
mw := middlewares[i]
handler = mw(handler)
```

The loop only steps through `middlewares`. `handler` is one variable carried between iterations: on the first iteration it is the original handler passed in (`srv.mux`), and on every later iteration it is the already-wrapped result of the previous one. Like `x = x + 1`, each step takes the current value, transforms it, and stores it back; the transform here wraps a handler instead of adding a number.

### Why the loop runs backwards

| Step | `i` | `handler` after the step |
|---|---|---|
| start | — | `mux` |
| 1 | 2 | `Logging(mux)` |
| 2 | 1 | `RequestID(Logging(mux))` |
| 3 | 0 | `Recovery(RequestID(Logging(mux)))` |

The last middleware wraps the handler first, and the first middleware ends up as the outermost layer. So the list reads in the order a request travels: **first in the list runs first**. A forward loop would build the stack in reverse order.

Without `Chain` you would write the nesting by hand: `Recovery(logger)(RequestID(Logging(logger)(mux)))`.

### Order matters

`Logging` reads the request ID with `GetRequestID(r.Context())`, and `RequestID` is what puts it in the context. `RequestID` must therefore come **before** `Logging` in the list; otherwise every request log line has an empty `request_id`.

## How a request flows through the stack

`next.ServeHTTP(w, r)` is an ordinary, synchronous function call: no goroutine, no callback. The calling middleware pauses on that line until the call returns, then continues with the next line, exactly like:

```go
func main() {
	fmt.Println("before")
	doWork()             // main waits here
	fmt.Println("after") // runs after doWork returns
}
```

So the whole chain is calls inside calls, a call stack:

```
Recovery
 └─ calls RequestID
     └─ calls Logging
         └─ calls mux
             └─ calls handleIngest
             ← returns (after writeJSON)
         ← mux returns
         Logging continues: logger.Info(...)
     ← Logging returns
 ← RequestID returns
← Recovery returns → Go's server finishes the response
```

On the way down, each middleware runs the code **before** its `next.ServeHTTP` call. On the way back up, each one runs the code **after** it. This is why middleware is often drawn as an onion: the request passes in through every layer and comes back out through the same layers in reverse.

- `Logging` uses both halves: before the call it starts the timer, after the call it reads the status and the duration.
- `Recovery` uses the return trip: its deferred `recover()` catches a panic as the stack unwinds back up to it.

## Capturing the status code: `wrappedWriter`

### The problem

`http.ResponseWriter` has no way to read the status back. The writer the handler normally receives is Go's internal `*http.response`; its `WriteHeader` sends the status to the client and keeps no getter. Once the handler returns, the middleware cannot ask what was sent.

The fix is to slip a wrapper in between the handler and the real writer, so every write passes through code that records the status first.

```go
type wrappedWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *wrappedWriter) WriteHeader(statusCode int) {
	if !w.written {
		w.statusCode = statusCode
		w.written = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *wrappedWriter) Write(data []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(data)
}
```

### The embedded field

`http.ResponseWriter` is **embedded**: it has a type but no field name written out. That gives `wrappedWriter` every method of the real writer automatically (method promotion). Only `WriteHeader` and `Write` are overridden; `Header()` goes straight to the real writer.

An embedded field is named after its type, without the package prefix:

| Embedded type | Field name |
|---|---|
| `http.ResponseWriter` | `ResponseWriter` |
| `sync.Mutex` | `Mutex` |
| `*bytes.Buffer` | `Buffer` (the `*` is dropped too) |

So `w.ResponseWriter` is the original writer, and the same name is used to build the struct: `wrappedWriter{ResponseWriter: w}`.

Inside the overrides, calling `w.ResponseWriter.WriteHeader(code)` is how Go does what other languages call `super.method()`. A plain `w.WriteHeader(code)` would call the override itself, which would call itself again forever.

### How the handler's calls reach the wrapper

`Logging` passes the wrapper down instead of the original writer:

```go
wrapped := wrappedWriter{
	ResponseWriter: w,
	statusCode:     http.StatusOK,
}

next.ServeHTTP(&wrapped, r)
```

The handler's parameter `w http.ResponseWriter` is an **interface**. An interface value remembers which concrete value it holds, and method calls go to that value's methods. Here it holds a `*wrappedWriter`:

```
w (http.ResponseWriter)
 └── actually holds: *wrappedWriter
```

So inside the handler:

- `w.WriteHeader(...)` runs `(*wrappedWriter).WriteHeader`.
- `w.Write(...)` runs `(*wrappedWriter).Write`.
- `w.Header()` is not overridden, so the embedded writer's `Header()` runs.

The handler does not know it is wrapped and does not need to change. That is the point of the pattern: one middleware records the status for every handler. Only requests that pass through `Logging` are wrapped; a route registered outside the chain receives the raw writer.

The `&` matters. The methods have pointer receivers (`w *wrappedWriter`), so:

- Only `*wrappedWriter` satisfies `http.ResponseWriter`. Passing the value would not compile.
- Changes made inside the methods land on the original `wrapped` variable in `Logging`. With a copy, `Logging` would still see `statusCode=200` after the handler returns.

### `statusCode: http.StatusOK` is a placeholder

The `200` is a starting value, not a forced status. The handler's real status replaces it. The default only stays if the handler calls `Write` without `WriteHeader`, or writes nothing at all, and in both cases Go sends `200 OK` anyway.

### The `written` flag

`written` starts as `false` because it is left out of the struct literal and `bool`'s zero value is `false`. Whichever of `WriteHeader` or `Write` runs first sets it to `true`. After that, `!w.written` is `false`, and both `if` blocks are skipped on every later call.

The flag means "the status is already decided". It matters because `writeJSON` calls `WriteHeader` and **then** `Write`:

```go
func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(v) // calls w.Write
}
```

Without the flag, `Write` would run `w.statusCode = http.StatusOK` and replace the real status, so every response would be logged as 200. With it:

- `Write` after `WriteHeader` keeps the real status.
- A second `WriteHeader` call does not change the recorded status, matching HTTP, where only the first status is sent.
- `Write` with no `WriteHeader` first records 200, which is what Go sends in that case.

## Trace: a `201` from `POST /ingest`

A valid body sent to `POST /ingest`. The chain is `Recovery → RequestID → Logging → mux → handleIngest`.

| # | Where | What happens | `statusCode` | `written` |
|---|---|---|---|---|
| 1 | `RequestID` | generates an ID (say `a1b2c3`) and puts it in the context | — | — |
| 2 | `Logging` | `start = time.Now()`, creates `wrapped` | 200 | false |
| 3 | `Logging` | `next.ServeHTTP(&wrapped, r)`: the handler gets the wrapper as `w` | 200 | false |
| 4 | `handleIngest` | content type OK, JSON parses, validation passes, `InsertLog` succeeds | 200 | false |
| 5 | `handleIngest` | logs `"log entry stored"` | 200 | false |
| 6 | `writeJSON` | `w.WriteHeader(201)`: `!written` is true, so it records 201 and forwards it | **201** | **true** |
| 7 | `writeJSON` | `w.Write(body)`: `!written` is false, so the `if` is skipped and the body is forwarded | 201 | true |
| 8 | `Logging` | handler has returned; reads `wrapped.statusCode` and logs | **201** | |

Output with the text handler (any environment other than `production`):

```
time=2026-10-24T10:15:02.114+02:00 level=INFO msg="log entry stored" request_id=a1b2c3 service=auth entry_level=error
time=2026-10-24T10:15:02.115+02:00 level=INFO msg="request completed" method=POST path=/ingest status_code=201 duration=3.41ms request_id=a1b2c3
```

- The handler's line prints first, because the handler runs inside `Logging`.
- `Logging`'s line prints after the handler returns, with `status_code=201`.
- Both lines share a `request_id` because `RequestID` sits outside `Logging`, which lets you match them up.

With an invalid body the handler calls `writeJSON(w, http.StatusBadRequest, ...)` instead, step 6 records 400, and the line reads `status_code=400`.

## Known gaps

- **Repeated `WriteHeader`:** a second call is still forwarded to the real writer, and Go logs `http: superfluous response.WriteHeader call`. Returning early when `written` is already true avoids it.
- **Hidden optional interfaces:** embedding promotes only the methods of the `ResponseWriter` interface, so the wrapper hides extras such as `http.Flusher` and `http.Hijacker` (streaming and SSE break). Adding `func (w *wrappedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }` lets `http.NewResponseController` reach the real writer.
- **Panics have no request ID:** `Recovery` is outside `RequestID`, so the `r` it logs from never got the ID in its context, and `request_id` is empty in `"recovered panic"` logs. Putting `RequestID` first in the chain fixes it.
