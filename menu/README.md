# sky10-menu

System tray / menu bar app for sky10. Three items:

- **Open Web UI** — opens `http://localhost:9101` in the default browser
- **Restart Daemon** — runs `sky10 daemon restart`
- **Quit** — stops the daemon and exits

Built with [Tauri v2](https://v2.tauri.app). Uses the system WebView —
no bundled browser engine. Binary is ~3MB.

## Build

Requires Rust. The binary is built automatically by CI on every release
tag — you don't need Rust locally.

```bash
cd menu/src-tauri
cargo build --release
```

The binary is at `target/release/sky10-menu`.

## Icons

Place icons in `src-tauri/icons/`. Tauri needs at minimum:
- `32x32.png`
- `128x128.png`
- `128x128@2x.png`
- `icon.icns` (macOS)
- `icon.ico` (Windows)

Generate from a source PNG with `cargo tauri icon source.png`.
