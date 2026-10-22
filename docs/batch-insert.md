# Batch Insert

How `Store.InsertBatch` (`internal/store/store.go`) works, and why it exists.

## Why batch?

Every `INSERT` sent on its own pays one full round trip to Postgres, plus a commit. Measured locally with `pgbench`, using one connection against the Docker `logline-db` container:

| Statement | Avg latency | Per second, one connection |
|---|---|---|
| `SELECT 1` (pure round trip) | 0.098 ms | ~10,200 |
| Single-row `INSERT` + commit | 0.142 ms | ~7,000 |

These numbers are **deceptively good**. The database runs on the same machine, so there is no real network, and the disk flush on commit (fsync) is cheap inside the Docker Desktop VM. In production the database is usually on another host (0.5–2 ms round trip) with real fsync (about 1–10 ms), so one connection drops to roughly 100–1,000 single-row inserts per second.

Batching sends **many rows in one statement**, so the round-trip and commit cost is paid once per batch instead of once per row.

### Measuring it yourself

```sh
# Pure round trip
echo 'SELECT 1;' > select1.sql
PGPASSWORD=logline pgbench -h localhost -p 5433 -U logline -n -c 1 -T 5 -f select1.sql logline

# Round trip + insert + commit (writes rows; delete them afterwards)
echo "INSERT INTO logs (level,message,service,timestamp) VALUES ('info','probe','rtt-probe',now());" > insert.sql
PGPASSWORD=logline pgbench -h localhost -p 5433 -U logline -n -c 1 -T 3 -f insert.sql logline
psql -h localhost -p 5433 -U logline -d logline -c "DELETE FROM logs WHERE service='rtt-probe'"
```

Read the `latency average` line. Other ways to measure:

- **In the app:** wrap the database call with `t := time.Now()` … `time.Since(t)`. This also includes time spent waiting for a free pool connection.
- **Database-side only:** `EXPLAIN ANALYZE` inside `BEGIN … ROLLBACK` (a single run is noisy), or `mean_exec_time` from the `pg_stat_statements` extension.

## The code

```go
func (s *Store) InsertBatch(ctx context.Context, entries []domain.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	var sqlBuilder strings.Builder
	sqlBuilder.WriteString("INSERT INTO logs (level, message, service, timestamp, data) VALUES ")

	args := make([]any, 0, len(entries)*5)

	for i, entry := range entries {
		if i > 0 {
			sqlBuilder.WriteString(", ")
		}

		data, err := json.Marshal(entry.Data)
		if err != nil {
			return fmt.Errorf("marshaling entry %d data: %w", i, err)
		}

		// $1-$5 for first row, $6-$10 for second, etc.
		offset := i * 5
		fmt.Fprintf(&sqlBuilder, "($%d,$%d,$%d,$%d,$%d)",
			offset+1, offset+2, offset+3, offset+4, offset+5)

		args = append(args, entry.Level, entry.Message, entry.Service, entry.Timestamp, data)
	}

	_, err := s.db.ExecContext(ctx, sqlBuilder.String(), args...)
	return err
}
```

For 2 entries it produces:

```sql
INSERT INTO logs (level, message, service, timestamp, data)
VALUES ($1,$2,$3,$4,$5), ($6,$7,$8,$9,$10)
```
```
args = ["info",  "msg A", "api", t1, `{"k":"v"}`,
        "error", "msg B", "web", t2, `null`]
```

## Line by line

```go
func (s *Store) InsertBatch(ctx context.Context, entries []domain.LogEntry) error {
```
A method on `Store`. It takes a context (for cancellation and timeouts) and a slice of entries, and returns an error, or `nil` on success.

```go
if len(entries) == 0 {
	return nil
}
```
Return early when there is nothing to insert. Without this check, the query would be `INSERT ... VALUES ` with nothing after it, which is a SQL syntax error.

```go
var sqlBuilder strings.Builder
sqlBuilder.WriteString("INSERT INTO logs (level, message, service, timestamp, data) VALUES ")
```
`strings.Builder` builds a string piece by piece without allocating a new string on every join, unlike `+` in a loop. This writes the fixed start of the query. The column list sets the order values must follow. `id` and `created_at` are omitted because the database fills them in.

```go
args := make([]any, 0, len(entries)*5)
```
This slice holds the actual values sent alongside the SQL. Its length starts at 0 and its capacity is `entries × 5` (5 columns per row), so it never has to grow during the loop. The type is `[]any` because the values are mixed (strings, a time, bytes).

```go
for i, entry := range entries {
```
Loop over the entries. `i` is the current index, which is needed to number the placeholders.

```go
if i > 0 {
	sqlBuilder.WriteString(", ")
}
```
Put a comma between rows, but not before the first: `(...), (...), (...)`.

```go
data, err := json.Marshal(entry.Data)
if err != nil {
	return fmt.Errorf("marshaling entry %d data: %w", i, err)
}
```
`entry.Data` is a `map[string]any` and the `data` column is JSONB, so it is converted to JSON bytes. On failure, the error names the entry that failed. `%w` wraps the original error so callers can still use `errors.Is` / `errors.As`.

```go
offset := i * 5
fmt.Fprintf(&sqlBuilder, "($%d,$%d,$%d,$%d,$%d)",
	offset+1, offset+2, offset+3, offset+4, offset+5)
```
Write this row's 5 placeholders. See [Why `offset := i * 5`](#why-offset--i--5) and [How `fmt.Fprintf` works](#how-fmtfprintf-works).

```go
args = append(args, entry.Level, entry.Message, entry.Service, entry.Timestamp, data)
```
Add this row's 5 values, in column order. `$1` maps to `args[0]`, `$2` to `args[1]`, and so on. The values travel separately from the SQL text, which is what prevents **SQL injection**: a message like `'); DROP TABLE logs;--` is stored as plain text and never runs as SQL.

```go
_, err := s.db.ExecContext(ctx, sqlBuilder.String(), args...)
return err
```
Send the SQL and all arguments to Postgres in **one call**. `args...` spreads the slice into separate arguments. The result (rows affected) is ignored and the error is returned. A single statement is atomic: either every row is inserted or none are.

## How the builder assembles the string

`strings.Builder` only **appends**. Each `WriteString` or `Fprintf` adds text to the end of the buffer, and nothing is ever replaced. Here is the buffer after each step, for 3 entries.

**Before the loop:**
```go
sqlBuilder.WriteString("INSERT INTO logs (level, message, service, timestamp, data) VALUES ")
```
```
INSERT INTO logs (level, message, service, timestamp, data) VALUES ␣
```
(The trailing space after `VALUES`, shown as `␣`, is intentional.)

**`i = 0`:** `i > 0` is false, so no comma. `Fprintf` appends row 0:
```
... VALUES ($1,$2,$3,$4,$5)
```

**`i = 1`:** `i > 0` is true, so `", "` is appended, then `Fprintf` appends row 1:
```
... VALUES ($1,$2,$3,$4,$5), ($6,$7,$8,$9,$10)
```

**`i = 2`:** `", "` is appended, then row 2:
```
... VALUES ($1,$2,$3,$4,$5), ($6,$7,$8,$9,$10), ($11,$12,$13,$14,$15)
```

**After the loop:** `sqlBuilder.String()` returns the whole buffer as one string, and that string is passed to `ExecContext`.

### Why there is no trailing comma

The separator is written *before* every row except the first, not *after* each row. Writing it after would leave `...($11,...,$15),` with a dangling comma, which is invalid SQL and would need trimming. "Separator before, skip the first" is the usual Go way to handle this.

### `args` grows alongside

| After | Highest placeholder | `len(args)` |
|---|---|---|
| `i = 0` | `$5` | 5 |
| `i = 1` | `$10` | 10 |
| `i = 2` | `$15` | 15 |

The highest placeholder number always equals `len(args)`. The builder only ever contains `$N` slots, never the values themselves. Postgres receives the SQL text and the argument list as two separate things.

## `i` vs `len(entries)`

`i` is **not** the number of entries. It is the index of the entry being processed *right now*.

```go
entries := []LogEntry{A, B, C}   // len(entries) = 3

for i, entry := range entries {
    // 1st time through: i = 0, entry = A
    // 2nd time through: i = 1, entry = B
    // 3rd time through: i = 2, entry = C
}
```

| | With 3 entries | Changes during the loop? |
|---|---|---|
| `len(entries)` | 3 | No |
| `i` | 0 → 1 → 2 | Yes, +1 each time |

`i` stops at `len(entries) - 1` because indexes start at 0.

## Why `offset := i * 5`

Placeholder numbers are counted across the **whole statement**, not restarted per row. Each row uses 5 of them (one per column), so before row `i` starts, the earlier rows have already used `i × 5` numbers. The offset is where row `i` starts counting.

| Row (`i`) | Numbers already used | `offset` | This row's placeholders |
|---|---|---|---|
| 0 | none | 0 | `$1 $2 $3 $4 $5` |
| 1 | $1–$5 | 5 | `$6 $7 $8 $9 $10` |
| 2 | $1–$10 | 10 | `$11 $12 $13 $14 $15` |
| 3 | $1–$15 | 15 | `$16 $17 $18 $19 $20` |

**Why every number must be unique:** `$N` means "use `args[N-1]`", and Postgres matches by number, not by position in the text. If every row reused `$1–$5`:

```sql
VALUES ($1,$2,$3,$4,$5), ($1,$2,$3,$4,$5), ($1,$2,$3,$4,$5)
```

every row would point at `args[0..4]`, inserting the first entry three times, and Postgres would reject the extra unused arguments. With the offset, each row points at its own section of `args`:

```
args:  [ L0 M0 S0 T0 D0 | L1 M1 S1 T1 D1 | L2 M2 S2 T2 D2 ]
        $1 ........ $5   $6 ....... $10   $11 ...... $15
          row 0             row 1             row 2
```

**The `5` is the column count** (`level, message, service, timestamp, data`). Adding a column means updating it in three places: the `offset`, the `Fprintf` format string, and the `make` capacity. A named constant helps:

```go
const colsPerRow = 5
offset := i * colsPerRow
```

## How `fmt.Fprintf` works

```go
func Fprintf(w io.Writer, format string, a ...any) (n int, err error)
```

It **formats** a template by filling its `%` slots with values, then **writes** the result to `w`, which can be anything that accepts bytes (an `io.Writer`). The name is **F** (to a writer, originally a file) + **print** + **f** (formatted).

### The three siblings

| Function | Where the result goes |
|---|---|
| `fmt.Printf(format, ...)` | stdout |
| `fmt.Sprintf(format, ...)` | returned as a string |
| `fmt.Fprintf(w, format, ...)` | written into `w` (file, connection, HTTP response, builder, …) |

The formatting rules are identical. Only the destination differs.

### Filling the template

Only `%` is special. Everything else, including `$`, `(`, `,` and `)`, is copied as-is. Each `%d` is a slot, filled **in order** by the values that follow.

With `offset = 5`:

```
template:  ( $ %d , $ %d , $ %d , $ %d , $ %d )
              ↑      ↑      ↑      ↑      ↑
values:       6      7      8      9      10
result:    "($6,$7,$8,$9,$10)"
```

So in `$%d`, `$` is the literal Postgres placeholder character and `%d` becomes the number.

- The number of slots must match the number of values. Too few values prints `%!d(MISSING)` and too many adds `%!(EXTRA ...)`. There is no compile error, but `go vet` catches it.
- `%%` prints a literal `%`.

### Common verbs

| Verb | Meaning | Example → output |
|---|---|---|
| `%d` | integer, decimal | `42` → `42` |
| `%s` | string | `"api"` → `api` |
| `%q` | quoted string | `"api"` → `"api"` |
| `%v` | default format for any value | `[]int{1,2}` → `[1 2]` |
| `%+v` | struct with field names | `{Level:info Service:api}` |
| `%T` | the value's type | `42` → `int` |
| `%.2f` | float, 2 decimal places | `3.14159` → `3.14` |
| `%x` | hexadecimal | `255` → `ff` |
| `%w` | wrap an error (`fmt.Errorf` only) | |

### Why `&sqlBuilder`?

`io.Writer` is any type with `Write(p []byte) (n int, err error)`. `strings.Builder` defines `Write` on the **pointer** type (`*strings.Builder`), because writing modifies the builder. Passing `sqlBuilder` without `&` does not compile.

### Why not `Sprintf` + `WriteString`?

```go
// works, but makes a throwaway string for every row
sqlBuilder.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d)", ...))

// writes directly into the builder
fmt.Fprintf(&sqlBuilder, "($%d,$%d,$%d,$%d,$%d)", ...)
```

`Sprintf` creates a new string, which is copied into the builder and then discarded. `Fprintf` skips that middle step, which saves one allocation per row (10,000 for a 10,000-row batch). The `(n, err)` return values are ignored because writing to a `strings.Builder` never fails.

## Gotchas

1. **Postgres allows at most 65,535 placeholders per statement.** At 5 per row, that is a maximum of **13,107 entries** per batch. Larger inputs fail, so callers must split them into chunks.
2. **Entries without `data` are stored as JSON `null`, not `{}`.** When the request has no `data` field, `Data` is a nil map, and `json.Marshal` of a nil map produces `null`. Because `data` is listed explicitly in the column list, the schema's `DEFAULT '{}'` never applies. To store `{}`, set `entry.Data = map[string]any{}` when it is nil, before marshaling.
