# sky10-menu

System tray / menu bar app for sky10. Three items:

- **Open Web UI** — opens `http://localhost:9101` in the default browser
- **Restart Daemon** — runs `sky10 daemon restart`
- **Quit** — stops the daemon and exits

Built with [Tauri v2](https://v2.tauri.app). Uses the system WebView —
no bundled browser engine. Binary is ~3MB.

## Build

Requires Rust. The binary is built automatically by CI on every release
tag — you don't need Rust locally. `menu/src-tauri/rust-toolchain.toml`
pins the exact Rust toolchain used for release builds, and
`menu/src-tauri/Cargo.lock` pins the dependency graph.

```bash
cd menu/src-tauri
cargo build --release --locked
```

The binary is at `target/release/sky10-menu`.

For a release-equivalent local build from the repo root:

```bash
make build-menu
```

To prove two clean local builds are byte-identical:

```bash
make reproduce-menu
```

## Icons

Place icons in `src-tauri/icons/`. Tauri needs at minimum:
- `32x32.png`
- `128x128.png`
- `128x128@2x.png`
- `icon.icns` (macOS)
- `icon.ico` (Windows)

Generate from a source PNG with `cargo tauri icon source.png`.
