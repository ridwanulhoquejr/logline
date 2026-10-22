# COPY Protocol (`Store.CopyFrom`)

How `Store.CopyFrom` (`internal/store/store.go`) bulk-loads log entries with Postgres's COPY protocol, and how its `[][]any` rows work.

See also: [batch-insert.md](batch-insert.md) for the multi-row `INSERT` approach (`InsertBatch`).

## What COPY is

`COPY` is Postgres's bulk-load command and the fastest way to insert many rows. Instead of building one big SQL string with placeholders (as `InsertBatch` does), the client sends a single `COPY logs (...) FROM STDIN` command and then streams every row in a compact binary format.

- Postgres doesn't parse SQL for each row.
- There are no `$N` placeholders, so there is no offset arithmetic and no 65,535-parameter limit.
- It is one statement: either all rows are inserted or none are.

The catch is that `database/sql`, the standard library package behind `s.db`, has no COPY support. The code has to reach underneath it to the raw pgx connection, which is what most of `CopyFrom` does.

## The code

```go
func (s *Store) CopyFrom(ctx context.Context, entries []domain.LogEntry) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquiring connection: %w", err)
	}
	defer conn.Close()

	var count int64
	err = conn.Raw(func(driverConn any) error {
		stdConn, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("expected *stdlib.Conn, got %T", driverConn)
		}

		pgxConn := stdConn.Conn()
		rows := make([][]any, len(entries))
		for i, entry := range entries {
			data, err := json.Marshal(entry.Data)
			if err != nil {
				return fmt.Errorf("marshaling entry %d: %w", i, err)
			}
			rows[i] = []any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data}
		}

		count, err = pgxConn.CopyFrom(
			ctx,
			pgx.Identifier{"logs"},
			[]string{"level", "message", "service", "timestamp", "data"},
			pgx.CopyFromRows(rows),
		)
		return err
	})

	return count, err
}
```

## Line by line

```go
func (s *Store) CopyFrom(ctx context.Context, entries []domain.LogEntry) (int64, error) {
```
Returns how many rows were inserted (`int64`) and an error.

```go
if len(entries) == 0 {
	return 0, nil
}
```
Nothing to insert, so return early. This also avoids borrowing a connection for no reason.

```go
conn, err := s.db.Conn(ctx)
if err != nil {
	return 0, fmt.Errorf("acquiring connection: %w", err)
}
```
`s.db` is a **pool** of connections (`*sql.DB`). Normally each query borrows any free connection and returns it immediately. `s.db.Conn(ctx)` instead **reserves one specific connection** (`*sql.Conn`) for this function, because the next step needs to work directly with that connection. If the pool is exhausted, this waits until a connection frees up or `ctx` is cancelled.

```go
defer conn.Close()
```
When the function returns, the connection goes **back to the pool**. Despite the name, `Close()` here doesn't close the network connection to Postgres. It only ends the reservation. Without it, the connection would stay reserved forever and the pool would eventually run dry.

```go
var count int64
```
Declared outside the callback so the callback can set it. `conn.Raw` only returns an `error`, so the count has to leave the callback through this variable.

```go
err = conn.Raw(func(driverConn any) error {
```
`Raw` gives the function the **driver's own connection object**, the thing `database/sql` wraps. It arrives typed as `any` because `database/sql` doesn't know which driver is in use. Two rules:
- `driverConn` is only safe to use **inside** the callback. Never keep it after the callback returns.
- The callback's returned error becomes `Raw`'s return value.

```go
stdConn, ok := driverConn.(*stdlib.Conn)
if !ok {
	return fmt.Errorf("expected *stdlib.Conn, got %T", driverConn)
}
```
A **type assertion**: "this `any` should really be a `*stdlib.Conn`." That holds because the app registers the pgx driver (`_ "github.com/jackc/pgx/v5/stdlib"` in `cmd/logline/main.go`). The comma-ok form (`, ok`) returns an error instead of panicking if the driver is ever changed. `%T` prints the actual type it got.

```go
pgxConn := stdConn.Conn()
```
Unwraps the stdlib wrapper to the native `*pgx.Conn`, which has the `CopyFrom` method.

```go
rows := make([][]any, len(entries))
```
Creates the table of rows: one empty spot for each entry. See [Understanding `[][]any`](#understanding-any) below.

```go
for i, entry := range entries {
	data, err := json.Marshal(entry.Data)
	if err != nil {
		return fmt.Errorf("marshaling entry %d: %w", i, err)
	}
```
`Data` is a `map[string]any` and the column is JSONB, so it is converted to JSON bytes. Returning an error exits the callback, so `Raw` returns it and nothing is sent to Postgres. The `:=` creates a **new** `err` that exists only inside the loop.

```go
	rows[i] = []any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data}
}
```
Builds one row with the values in column order and places it in spot `i`.

```go
count, err = pgxConn.CopyFrom(
	ctx,
	pgx.Identifier{"logs"},
	[]string{"level", "message", "service", "timestamp", "data"},
	pgx.CopyFromRows(rows),
)
```
Runs the COPY. The arguments:
- `ctx`: cancelling it aborts the copy partway.
- `pgx.Identifier{"logs"}`: the table name, safely quoted as `"logs"`. Use `pgx.Identifier{"public", "logs"}` to name a schema.
- `[]string{...}`: the columns to fill. They must match the order of values in each row.
- `pgx.CopyFromRows(rows)`: adapts `[][]any` into the interface `CopyFrom` reads rows from.

pgx first asks Postgres for the column types, so it knows how to binary-encode each value. Then it streams every row and returns the number of rows written.

Note `=`, not `:=`. The loop's `err` no longer exists here, so this is the **outer** `err` (from `conn, err := s.db.Conn(ctx)`), which the callback can see. Same for `count`. This is how the result gets out of the callback.

```go
	return err
})

return count, err
```
The callback's error becomes `Raw`'s return value and is assigned to `err`. The function returns the count and error, then the deferred `conn.Close()` returns the connection to the pool.

## Order of events

```
1. Reserve one connection from the pool          s.db.Conn
2. Get the raw pgx connection out of it          conn.Raw + type assertion
3. Build [][]any rows (JSON-encode data)         loop
4. COPY logs (...) FROM STDIN                    pgxConn.CopyFrom
     → ask Postgres for column types
     → stream all rows in binary
     → Postgres replies "COPY <n>"
5. Release the connection back to the pool       defer conn.Close
```

## Understanding `[][]any`

These are the questions that came up while reading this code, with the answers that made it click.

### "Outer slice / inner slice" didn't make sense. What is `[][]any`?

Build it from the bottom up.

**One log entry is a list of 5 things.** `[]any` is just a list:

```go
row0 := []any{"info", "msg A", "api", t1, data}
```

**Three log entries are three lists:**

```go
row0 := []any{"info",  "msg A", "api",  t1, data}
row1 := []any{"error", "msg B", "web",  t2, data}
row2 := []any{"warn",  "msg C", "auth", t3, data}
```

**Put those three lists into one list:**

```go
rows := [][]any{row0, row1, row2}
```

`[][]any` is **a list whose items are lists**. Read the type left to right:

| Type | Read as |
|---|---|
| `[]int` | a list of ints |
| `[]any` | a list of anything |
| `[][]any` | a list of (lists of anything) |

**Picture a cart of bags:**

- Each **bag** holds 5 items: level, message, service, timestamp, data. That's one `[]any`, one log entry.
- The **cart** holds all the bags. That's `[][]any`, all the log entries.

```
cart (rows)
 ├── bag 0:  "info",  "msg A", "api",  t1, data
 ├── bag 1:  "error", "msg B", "web",  t2, data
 └── bag 2:  "warn",  "msg C", "auth", t3, data
```

- `rows[1]` means "take bag 1 out of the cart" → `["error", "msg B", "web", t2, data]`
- `rows[1][2]` means "from bag 1, take item 2" → `"web"`

**The same idea with plain numbers:**

```go
a := []int{1, 2, 3}
b := []int{4, 5, 6}
both := [][]int{a, b}

fmt.Println(both)        // [[1 2 3] [4 5 6]]
fmt.Println(both[1])     // [4 5 6]
fmt.Println(both[1][0])  // 4
```

### So is it a 2D matrix, like a table?

Yes. `rows[i][j]` means row `i`, column `j`:

```
            j=0      j=1      j=2     j=3   j=4
          level    message  service   ts    data
i=0  →  [ "info",  "msg A",  "api",   t1,   {...} ]
i=1  →  [ "error", "msg B",  "web",   t2,   null  ]
i=2  →  [ "warn",  "msg C",  "auth",  t3,   {}    ]
```

One difference from a true matrix: Go doesn't force every row to be the same length. Each inner list is independent and could hold 3 values or 7. This is called a "jagged" array. Here every row has exactly 5 values because the code always builds them that way, and pgx requires each row's length to match the column count.

A fixed-size array like `[3][5]any` would guarantee the shape, but array sizes must be known at compile time. The number of entries changes with each request, so a slice of slices is the right choice.

### Why `any` and not a specific type?

A Go slice holds **one type**, but each row mixes types:

| Column | Go type |
|---|---|
| level, message, service | `string` |
| timestamp | `time.Time` |
| data | `[]byte` |

`[]string` can't hold a `time.Time`. `any` (an alias for `interface{}`) accepts every type, so all 5 values fit in one list. pgx then looks at each value's real type, together with the column type it got from Postgres, and encodes it correctly.

### Why not pass `[]domain.LogEntry` directly?

pgx is a general-purpose library and knows nothing about the `LogEntry` struct. It can't tell which field goes into which column, or that `Data` needs JSON encoding. The code does that translation itself (struct → `[]any` in column order), and pgx just matches position to position:

```
columns:  "level"  "message"  "service"  "timestamp"  "data"
             ↓         ↓          ↓           ↓          ↓
row:       "info"   "msg A"     "api"        t1     {"k":"v"}
```

### Why does `CopyFromRows` expect `[][]any`?

pgx reads COPY data **one row at a time**, and to pgx a row is a `[]any`. `CopyFrom` doesn't take `[][]any` directly. It takes this interface:

```go
type CopyFromSource interface {
    Next() bool              // is there another row?
    Values() ([]any, error)  // give me that row
    Err() error              // did anything go wrong?
}
```

pgx loops: `Next()`, then `Values()` to send one row, then repeat until `Next()` returns false.

`CopyFromRows` is a small adapter that turns "many rows" into that interface. This is essentially pgx's own source:

```go
func CopyFromRows(rows [][]any) CopyFromSource {
    return &copyFromRows{rows: rows, idx: -1}
}

func (c *copyFromRows) Next() bool             { c.idx++; return c.idx < len(c.rows) }
func (c *copyFromRows) Values() ([]any, error) { return c.rows[c.idx], nil }
func (c *copyFromRows) Err() error             { return nil }
```

- **Why `[]any` for one row:** pgx must work for any table, with any columns of any types, so mixed values have to be `[]any`.
- **Why `[][]any` for the input:** a COPY inserts many rows, and many `[]any` rows make a `[][]any`.

### How does the loop fill it?

**Step 1: create the cart with empty spots.**

```go
rows := make([][]any, len(entries))   // with 3 entries:
```
```
rows[0] → nil
rows[1] → nil
rows[2] → nil
```

**Step 2: each pass packs one bag and puts it in spot `i`.**

```go
rows[i] = []any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data}
```

That one line does two things. Written out:

```go
bag := []any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data}  // 1. pack the bag
rows[i] = bag                                                                    // 2. put it in spot i
```

`[]any{a, b, c, d, e}` is a **composite literal**: it creates a new list from the values in the braces.

Trace with 3 entries:

```
i = 0:  rows = [ ["info","msg A","api",t1,data],  nil,                               nil ]
i = 1:  rows = [ [...info...],                     ["error","msg B","web",t2,data],   nil ]
i = 2:  rows = [ [...info...],                     [...error...],                     ["warn","msg C","auth",t3,data] ]
```

What one pass does:

```
entries[i]  (a LogEntry struct)
   │
   ├─ .Level      ──────────────────┐
   ├─ .Message    ──────────────────┤
   ├─ .Service    ──────────────────┤──►  []any{ ... }  ──►  rows[i]
   ├─ .Timestamp  ──────────────────┤
   └─ .Data ── json.Marshal ─► data ┘
```

The longhand, cell-by-cell equivalent:

```go
rows[i] = make([]any, 5)
rows[i][0] = entry.Level
rows[i][1] = entry.Message
rows[i][2] = entry.Service
rows[i][3] = entry.Timestamp
rows[i][4] = data
```

### Is that appending?

No, it's **placing**. `make([][]any, N)` already created N empty spots, and `rows[i] = ...` fills an existing spot. The `append` style starts with an empty cart instead:

```go
rows := make([][]any, 0, len(entries))   // no spots yet, room for N
for _, entry := range entries {
    rows = append(rows, []any{entry.Level, entry.Message, entry.Service, entry.Timestamp, data})
}
```

| Cart starts as | How to add a bag |
|---|---|
| `make([][]any, N)`: N empty spots | `rows[i] = bag` |
| `make([][]any, 0, N)`: no spots, room for N | `rows = append(rows, bag)` |

Both are correct. **Mixing them is a bug:** preset length plus `append` gives 2N spots with N `nil` rows at the front, and pgx fails on them.

### So the number of rows equals the number of entries?

Yes. One entry becomes one row, so `len(rows) == len(entries)`, and every row has 5 values:

| entries | rows | values in each row |
|---|---|---|
| 3 | 3 | 5 |
| 1,000 | 1,000 | 5 |

The returned `count` is the number of rows Postgres inserted, so on success `count == len(entries)`. If any row fails, the whole COPY is cancelled and nothing is inserted.

## `CopyFrom` vs `InsertBatch`

| | `InsertBatch` | `CopyFrom` |
|---|---|---|
| Mechanism | One big `INSERT ... VALUES` | `COPY ... FROM STDIN` |
| Placeholders | `$1…$N`, offset arithmetic | None |
| Row limit | 13,107 (65,535 params ÷ 5) | No placeholder limit |
| Postgres parsing | One huge SQL string | Binary row stream |
| Speed for large batches | Good | Best |
| Works through `database/sql` alone | Yes | No, needs the raw pgx connection |
| All-or-nothing | Yes | Yes |

The values are the same. Only their shape differs:

```
InsertBatch:  []any    →  [ L0 M0 S0 T0 D0  L1 M1 S1 T1 D1  L2 M2 S2 T2 D2 ]
                           one flat list; rows found by offset math

CopyFrom:     [][]any  →  [ [L0 M0 S0 T0 D0], [L1 M1 S1 T1 D1], [L2 M2 S2 T2 D2] ]
                           already grouped by row; no offset needed
```

`INSERT` needs one flat list because placeholders `$1…$15` are numbered across the whole statement. COPY sends data row by row, so grouping by row fits naturally.

## Things to watch

1. **The batch is held in memory twice:** once as `entries` and once as `rows`. `pgx.CopyFromSlice` implements the same interface but builds each row only when pgx asks for it:
   ```go
   pgx.CopyFromSlice(len(entries), func(i int) ([]any, error) {
       data, err := json.Marshal(entries[i].Data)
       if err != nil {
           return nil, err
       }
       return []any{entries[i].Level, entries[i].Message, entries[i].Service, entries[i].Timestamp, data}, nil
   })
   ```
2. **Missing `data` is stored as JSON `null`, not `{}`.** This is the same gotcha as `InsertBatch`: `json.Marshal` of a nil map produces `null`, and the explicit column list bypasses the schema's `DEFAULT '{}'`. To store `{}`, set `entry.Data = map[string]any{}` when it is nil.
