---
updated: 2026-03-14
---

# CLAUDE.md

<INSTRUCTIONS>

## Project Overview
- Repository: `sky10`
- Language: Go
- Purpose: Document agent collaboration guidelines and Go style for this repo.

## General Rules
- Prefer minimal, targeted changes.
- Keep outputs concise and actionable.
- Use `rg` for search when possible.
- Avoid destructive commands unless explicitly requested.
- Do not revert unrelated changes.
- Commit and push after every completed task. Don't let work pile up. After
  each command the user gives you, commit and push immediately. Never commit
  without pushing.

## Workflow
- Inspect existing files before editing.
- Explain what you changed and why.
- Call out any commands you could not run.

## Documentation
- When you learn something worth preserving — a decision, a gotcha, a design
  tradeoff, a library evaluation — write it up in `docs/learned/`. These are
  short, focused files that capture institutional knowledge for future
  reference.
- Implementation plans and work tracking go in `docs/work/`.

## Testing
- All code must be well tested. Write tests as you write code, not after.
- Use table-driven tests for functions with multiple input/output cases.
- Every exported function needs test coverage.
- Run `go test ./...` before committing. Tests must pass.
- Integration tests that require external services (S3, etc.) should be
  skippable via build tags or environment checks, but must exist.
- If tests are skipped, state why.

## Idiomatic Go Style Guide

All Go code in this repository must follow idiomatic Go conventions. These
guidelines are derived from studying production Go codebases (Caddy, Ollama,
CockroachDB) and the Go community's established best practices.

### Interfaces

- Keep interfaces small. One or two methods is ideal. Three is acceptable. More
  than three is a code smell.
- Define interfaces where they are consumed, not where they are implemented.
  The package that depends on the behavior should own the interface.
- Use interface composition to build larger contracts from small ones:
  ```go
  type ReadCloser interface {
      Reader
      Closer
  }
  ```
- Accept interfaces, return concrete types. Functions should take the narrowest
  interface they need and return the specific type they produce.
- Never export an interface just to match a single concrete type. If there's
  only one implementation, use the concrete type directly.

### Naming

- Package names are short, lowercase, single-word. No underscores, no camelCase.
  Good: `auth`, `format`, `kv`. Bad: `authService`, `string_utils`.
- Package names should not stutter with their contents. Use `http.Client`, not
  `http.HTTPClient`.
- Short variable names in small scopes. `r` for a reader in a 5-line function
  is fine. `reader` in a 50-line function is better.
- Exported names get descriptive names. Unexported names can be terse.
- Acronyms are all-caps: `ID`, `URL`, `HTTP`, `API`. Not `Id`, `Url`.
- Error variables use `Err` prefix: `ErrNotFound`, `ErrTimeout`.
- Error types use `Error` suffix: `StatusError`, `ValidationError`.
- Interface names use `-er` suffix when single-method: `Reader`, `Closer`,
  `Handler`. Multi-method interfaces describe the capability: `Provisioner`,
  `Validator`.
- Constructor functions: `New<Type>()` returns a pointer. `NewClient()`,
  `NewServer()`. If there's only one type in the package, `New()` is fine.

### Error Handling

- Always check errors immediately. Never ignore them unless assigning to `_`
  with a comment explaining why.
- Return `error` as the last return value. Always.
- Wrap errors with context using `fmt.Errorf("doing thing: %w", err)`. The `%w`
  verb allows callers to unwrap and inspect.
- Use sentinel errors (`var ErrNotFound = errors.New("not found")`) for errors
  that callers need to check with `errors.Is`.
- Use custom error types when callers need to extract information:
  ```go
  type StatusError struct {
      StatusCode int
      Message    string
  }
  func (e StatusError) Error() string { return e.Message }
  ```
- Check errors with `errors.Is` and `errors.As`, not type assertions.
- Do not log and return an error. Do one or the other, not both.
- Do not wrap errors that you created. Only wrap errors from callees.

### Functions and Methods

- `context.Context` is always the first parameter. Never store it in a struct.
- Keep functions short and focused. If a function needs a comment explaining
  what a section does, that section should probably be its own function.
- Use named return values only for documentation in godoc, or in short functions
  where a bare `return` improves clarity. In longer functions, always return
  explicitly.
- Prefer synchronous APIs. Only use channels and goroutines when concurrency is
  genuinely needed.
- Avoid `init()` functions. They make code harder to test and reason about.
  Initialize explicitly in `main()` or constructors. The only acceptable use is
  for registering plugins/drivers (the `database/sql` pattern).

### Structs

- Keep structs focused. A struct with 20+ fields is a design smell. Consider
  splitting into smaller, composable types or grouping related fields into
  sub-structs.
- Use struct embedding for composition, not inheritance. Embed to promote
  methods, not to hide implementation.
- Constructors should validate and return errors rather than panicking.
- Prefer value receivers for small structs and pointer receivers for large
  structs or when the method mutates state. Be consistent within a type.
- Zero values should be useful. Design structs so that `var x MyType` produces
  a valid, usable value when possible.

### Concurrency

- Do not start goroutines without a clear way to stop them. Every goroutine
  must have a termination condition (context cancellation, channel close, or
  WaitGroup).
- Use `context.Context` for cancellation and timeouts. Pass it through the
  call chain.
- Protect shared state with `sync.Mutex`. Document what the mutex protects:
  ```go
  // mu protects the fields below.
  mu      sync.Mutex
  clients map[string]*Client
  ```
- Prefer `sync.RWMutex` when reads vastly outnumber writes.
- Use channels for communication between goroutines. Use mutexes for protecting
  shared state.
- Use `errgroup.Group` from `golang.org/x/sync/errgroup` for managing groups
  of goroutines that can fail.
- Use `sync.Once` for one-time initialization.
- Never use `time.Sleep` in production code for synchronization. Use channels,
  timers, or condition variables.

### Package Structure

- Everything under one module. Do not create multiple Go modules in one repo
  unless you have a strong reason.
- Use `internal/` for packages that must not be imported by external code.
- Use `cmd/` for main packages (executables).
- Avoid a top-level `pkg/` directory. It adds noise without benefit in module-
  aware Go.
- Package by domain, not by layer. Group by what the code does (`auth`, `kv`,
  `manifest`), not by what it is (`models`, `controllers`, `utils`).
- Never create a `utils` or `helpers` package. Put functions in the package
  that uses them, or create a domain-specific package.
- Keep package dependency graphs acyclic and shallow.

### Standard Library First

- Use `net/http` for HTTP servers and clients. Do not reach for Gin, Echo, or
  other frameworks unless the project has already adopted one.
- Use `encoding/json` for JSON. Use `log/slog` for structured logging.
- Use `errors` from the standard library, with `errors.Is`, `errors.As`, and
  `fmt.Errorf` with `%w`.
- Use `testing` and table-driven tests. Do not reach for third-party test
  frameworks unless the project has already adopted one (e.g., `testify`).
- Use `context` for request-scoped values, cancellation, and timeouts.
- Only add a dependency when the stdlib genuinely cannot do the job or when
  the dependency is well-maintained and widely used.

### Testing

- Use table-driven tests for any function with multiple input/output cases:
  ```go
  tests := []struct {
      name string
      input string
      want  string
  }{
      {"empty", "", ""},
      {"single", "a", "A"},
  }
  for _, tt := range tests {
      t.Run(tt.name, func(t *testing.T) {
          got := Transform(tt.input)
          if got != tt.want {
              t.Errorf("Transform(%q) = %q, want %q", tt.input, got, tt.want)
          }
      })
  }
  ```
- Use `t.Helper()` in test helper functions so that test failures report the
  caller's line number.
- Use `t.Parallel()` where tests are independent.
- Use `t.Cleanup()` for teardown instead of `defer` when possible.
- Use `httptest.NewServer` for HTTP integration tests.
- Use `t.Setenv()` instead of `os.Setenv()` in tests.
- Test behavior, not implementation. Test the public API of a package.
- Test files live next to the code they test: `foo.go` and `foo_test.go` in
  the same directory.

### Comments and Documentation

- Every exported type, function, method, and constant must have a godoc
  comment starting with the name of the thing being documented.
- Comments explain *why*, not *what*. The code shows what; the comment explains
  the reasoning, tradeoffs, or non-obvious behavior.
- Do not add comments that restate the code. `// increment i` above `i++` is
  noise.
- Use `// TODO:` for known improvements. Include context about what and why.
- Package comments go in a `doc.go` file or at the top of the primary file.
- When code is subtle or relies on non-obvious invariants, add a comment
  explaining the invariant.

### JSON and Config

- Use `json:"field_name,omitempty"` struct tags consistently. Use snake_case
  for JSON field names.
- Use `json.RawMessage` for deferred or polymorphic JSON decoding.
- Validate configuration eagerly at load time, not lazily at use time.
- Provide sensible defaults. Zero values should work without configuration
  where possible.

### Files and Formatting

- Run `gofmt` (or `goimports`) on all code. No exceptions.
- One package per directory. One directory per package.
- Keep files focused. If a file exceeds ~500 lines, consider splitting by
  concern. A 2000-line file is almost always too large.
- Group imports in three blocks: stdlib, external, internal. `goimports`
  handles this automatically.
- File names are lowercase with underscores: `reverse_proxy.go`,
  `server_test.go`. Use `_test.go` suffix for test files. Use build tags
  in file names where appropriate: `listen_unix.go`, `service_windows.go`.

### What to Avoid

- Do not use `panic` for error handling. `panic` is for truly unrecoverable
  programmer errors (violated invariants, impossible states). Never panic on
  bad input or runtime errors.
- Do not use global mutable state. If you need singletons, pass them
  explicitly via constructors or context.
- Do not use `interface{}` / `any` when a concrete type or a specific
  interface will do. Generics (Go 1.18+) may also be appropriate.
- Do not prematurely abstract. Three similar lines are better than one
  abstraction used three times if the abstraction adds complexity.
- Do not create deep package hierarchies for small projects. Flat is better
  than nested until you have a reason to nest.
- Do not use getters and setters for simple field access. Go is not Java.
  Exported fields are fine for configuration structs.
- Do not over-use channels. A mutex is simpler and faster for protecting
  shared state. Channels are for communication, mutexes are for
  synchronization.
- Do not use `reflect` unless building frameworks, serialization, or
  plugin systems. If you reach for reflect, ask if there's a simpler way.

</INSTRUCTIONS>
