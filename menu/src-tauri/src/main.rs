#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::io::{Read, Write};
use std::net::TcpStream;
use std::process::Command;
use std::sync::atomic::{AtomicU32, Ordering};
use tauri::{
    menu::{MenuBuilder, MenuItemBuilder, PredefinedMenuItem},
    tray::TrayIconBuilder,
    Manager,
};

static RPC_ID: AtomicU32 = AtomicU32::new(1);

/// Call a JSON-RPC method on the daemon's HTTP endpoint.
/// Uses raw TCP to avoid pulling in an HTTP client crate.
fn rpc(method: &str) -> Option<String> {
    let id = RPC_ID.fetch_add(1, Ordering::Relaxed);
    let body = format!(
        r#"{{"jsonrpc":"2.0","method":"{}","id":{}}}"#,
        method, id
    );
    let req = format!(
        "POST /rpc HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
        body.len(),
        body
    );

    let mut stream = TcpStream::connect("127.0.0.1:9101").ok()?;
    stream.set_read_timeout(Some(std::time::Duration::from_secs(3))).ok()?;
    stream.write_all(req.as_bytes()).ok()?;

    let mut response = String::new();
    stream.read_to_string(&mut response).ok()?;

    // Skip HTTP headers — body starts after \r\n\r\n.
    let body_start = response.find("\r\n\r\n")? + 4;
    Some(response[body_start..].to_string())
}

/// Extract a string field from a JSON blob.
fn json_str<'a>(json: &'a str, field: &str) -> Option<&'a str> {
    let needle = format!("\"{}\"", field);
    let idx = json.find(&needle)? + needle.len();
    let rest = &json[idx..];
    let start = rest.find('"')? + 1;
    let end = start + rest[start..].find('"')?;
    Some(&rest[start..end])
}

struct DaemonInfo {
    version: String,
    update_available: bool,
    latest_version: String,
}

fn query_daemon() -> DaemonInfo {
    let mut info = DaemonInfo {
        version: "not running".to_string(),
        update_available: false,
        latest_version: String::new(),
    };

    // Get current version from health RPC.
    if let Some(resp) = rpc("skyfs.health") {
        if let Some(v) = json_str(&resp, "version") {
            info.version = v.split_whitespace().next().unwrap_or(v).to_string();
        }
    }

    // Check for updates via daemon RPC.
    if let Some(resp) = rpc("system.checkUpdate") {
        if resp.contains("\"available\":true") {
            info.update_available = true;
            if let Some(latest) = json_str(&resp, "latest") {
                info.latest_version = latest.to_string();
            }
        }
    }

    info
}

fn open_ui() {
    #[cfg(target_os = "macos")]
    { let _ = Command::new("open").arg("http://localhost:9101").spawn(); }

    #[cfg(target_os = "linux")]
    { let _ = Command::new("xdg-open").arg("http://localhost:9101").spawn(); }

    #[cfg(target_os = "windows")]
    { let _ = Command::new("cmd").args(["/C", "start", "http://localhost:9101"]).spawn(); }
}

fn restart_daemon() {
    let _ = Command::new("sky10").args(["daemon", "restart"]).spawn();
}

fn stop_daemon() {
    let _ = Command::new("sky10").args(["daemon", "stop"]).spawn();
}

fn main() {
    tauri::Builder::default()
        .setup(|app| {
            let info = query_daemon();

            let version_label = format!("sky10 {}", info.version);
            let version_item = MenuItemBuilder::with_id("version", &version_label)
                .enabled(false)
                .build(app)?;
            let sep1 = PredefinedMenuItem::separator(app)?;
            let open = MenuItemBuilder::with_id("open", "Open").build(app)?;
            let sep2 = PredefinedMenuItem::separator(app)?;
            let quit = MenuItemBuilder::with_id("quit", "Quit").build(app)?;

            let mut menu = MenuBuilder::new(app)
                .item(&version_item)
                .item(&sep1)
                .item(&open);

            if info.update_available {
                let update_label = format!("Update available ({})", info.latest_version);
                let update_item = MenuItemBuilder::with_id("update_info", &update_label)
                    .enabled(false)
                    .build(app)?;
                let restart_item = MenuItemBuilder::with_id("restart_update", "Restart to update")
                    .build(app)?;
                let sep3 = PredefinedMenuItem::separator(app)?;
                menu = menu.item(&sep3).item(&update_item).item(&restart_item);
            }

            let menu = menu.item(&sep2).item(&quit).build()?;

            TrayIconBuilder::new()
                .icon(app.default_window_icon().unwrap().clone())
                .menu(&menu)
                .tooltip("sky10")
                .on_menu_event(|app, event| match event.id().as_ref() {
                    "open" => open_ui(),
                    "restart_update" => restart_daemon(),
                    "quit" => {
                        stop_daemon();
                        app.exit(0);
                    }
                    _ => {}
                })
                .build(app)?;

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error running sky10-menu");
}
