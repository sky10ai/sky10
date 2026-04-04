#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::io::{Read, Write};
use std::net::TcpStream;
use std::process::Command;
use std::sync::atomic::{AtomicU32, Ordering};
use std::time::Duration;
use tauri::{
    image::Image,
    menu::{MenuBuilder, MenuItemBuilder, PredefinedMenuItem},
    tray::{TrayIcon, TrayIconBuilder},
    Manager,
};

static RPC_ID: AtomicU32 = AtomicU32::new(1);

// Tray icon SVGs embedded at compile time.
const ICON_CONNECTED: &[u8] = include_bytes!("../icons/tray/connected.svg");
const ICON_DISCONNECTED: &[u8] = include_bytes!("../icons/tray/disconnected.svg");
const ICON_UPDATE: &[u8] = include_bytes!("../icons/tray/update.svg");
const ICON_SYNCING: &[u8] = include_bytes!("../icons/tray/syncing.svg");
const ICON_ERROR: &[u8] = include_bytes!("../icons/tray/error.svg");

// --- RPC helpers ---

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
    stream
        .set_read_timeout(Some(Duration::from_secs(3)))
        .ok()?;
    stream.write_all(req.as_bytes()).ok()?;

    let mut response = String::new();
    stream.read_to_string(&mut response).ok()?;

    let body_start = response.find("\r\n\r\n")? + 4;
    Some(response[body_start..].to_string())
}

fn json_str<'a>(json: &'a str, field: &str) -> Option<&'a str> {
    let needle = format!("\"{}\"", field);
    let idx = json.find(&needle)? + needle.len();
    let rest = &json[idx..];
    let start = rest.find('"')? + 1;
    let end = start + rest[start..].find('"')?;
    Some(&rest[start..end])
}

fn json_int(json: &str, field: &str) -> Option<i64> {
    let needle = format!("\"{}\":", field);
    let idx = json.find(&needle)? + needle.len();
    let rest = json[idx..].trim_start();
    let end = rest.find(|c: char| !c.is_ascii_digit() && c != '-').unwrap_or(rest.len());
    rest[..end].parse().ok()
}

// --- Daemon state ---

#[derive(Clone, PartialEq)]
enum TrayState {
    Connected,
    Disconnected,
    Syncing,
    UpdateAvailable,
    Error,
}

struct DaemonInfo {
    version: String,
    state: TrayState,
    latest_version: String,
}

fn query_daemon() -> DaemonInfo {
    let mut info = DaemonInfo {
        version: "not running".to_string(),
        state: TrayState::Disconnected,
        latest_version: String::new(),
    };

    let health = match rpc("skyfs.health") {
        Some(h) => h,
        None => return info,
    };

    // Daemon is reachable.
    if let Some(v) = json_str(&health, "version") {
        info.version = v.split_whitespace().next().unwrap_or(v).to_string();
    }

    // Check if syncing (outbox_pending > 0).
    if let Some(pending) = json_int(&health, "outbox_pending") {
        if pending > 0 {
            info.state = TrayState::Syncing;
            return info;
        }
    }

    // Check for updates.
    if let Some(update_resp) = rpc("system.checkUpdate") {
        if update_resp.contains("\"available\":true") {
            info.state = TrayState::UpdateAvailable;
            if let Some(latest) = json_str(&update_resp, "latest") {
                info.latest_version = latest.to_string();
            }
            return info;
        }
    }

    info.state = TrayState::Connected;
    info
}

// --- Actions ---

fn open_ui() {
    #[cfg(target_os = "macos")]
    {
        let _ = Command::new("open")
            .arg("http://localhost:9101")
            .spawn();
    }
    #[cfg(target_os = "linux")]
    {
        let _ = Command::new("xdg-open")
            .arg("http://localhost:9101")
            .spawn();
    }
    #[cfg(target_os = "windows")]
    {
        let _ = Command::new("cmd")
            .args(["/C", "start", "http://localhost:9101"])
            .spawn();
    }
}

fn restart_daemon() {
    let _ = Command::new("sky10")
        .args(["daemon", "restart"])
        .spawn();
}

fn stop_daemon() {
    let _ = Command::new("sky10").args(["daemon", "stop"]).spawn();
}

// --- Menu building ---

fn build_menu(app: &tauri::App, info: &DaemonInfo) -> tauri::Result<tauri::menu::Menu<tauri::Wry>> {
    let version_label = format!("sky10 {}", info.version);
    let version_item = MenuItemBuilder::with_id("version", &version_label)
        .enabled(false)
        .build(app)?;
    let sep1 = PredefinedMenuItem::separator(app)?;

    let open_enabled = info.state != TrayState::Disconnected;
    let open = MenuItemBuilder::with_id("open", "Open")
        .enabled(open_enabled)
        .build(app)?;

    let sep2 = PredefinedMenuItem::separator(app)?;
    let quit = MenuItemBuilder::with_id("quit", "Quit").build(app)?;

    let mut menu = MenuBuilder::new(app)
        .item(&version_item)
        .item(&sep1)
        .item(&open);

    if info.state == TrayState::UpdateAvailable {
        let update_label = format!("Update available ({})", info.latest_version);
        let update_item = MenuItemBuilder::with_id("update_info", &update_label)
            .enabled(false)
            .build(app)?;
        let restart_item =
            MenuItemBuilder::with_id("restart_update", "Restart to update").build(app)?;
        let sep3 = PredefinedMenuItem::separator(app)?;
        menu = menu.item(&sep3).item(&update_item).item(&restart_item);
    }

    menu.item(&sep2).item(&quit).build()
}

fn icon_for_state(state: &TrayState) -> &'static [u8] {
    match state {
        TrayState::Connected => ICON_CONNECTED,
        TrayState::Disconnected => ICON_DISCONNECTED,
        TrayState::Syncing => ICON_SYNCING,
        TrayState::UpdateAvailable => ICON_UPDATE,
        TrayState::Error => ICON_ERROR,
    }
}

fn set_tray_state(tray: &TrayIcon, state: &TrayState) {
    let svg = icon_for_state(state);
    if let Ok(img) = Image::from_bytes(svg) {
        let _ = tray.set_icon(Some(img));
    }

    let tooltip = match state {
        TrayState::Connected => "sky10",
        TrayState::Disconnected => "sky10 (not running)",
        TrayState::Syncing => "sky10 (syncing)",
        TrayState::UpdateAvailable => "sky10 (update available)",
        TrayState::Error => "sky10 (error)",
    };
    let _ = tray.set_tooltip(Some(tooltip));
}

// --- Main ---

fn main() {
    tauri::Builder::default()
        .setup(|app| {
            let info = query_daemon();

            let menu = build_menu(app, &info)?;

            let tray = TrayIconBuilder::new()
                .icon(Image::from_bytes(icon_for_state(&info.state))?)
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

            set_tray_state(&tray, &info.state);

            // Poll daemon state every 30 seconds, update icon.
            let tray_clone = tray.clone();
            std::thread::spawn(move || {
                let mut prev_state = info.state.clone();
                loop {
                    std::thread::sleep(Duration::from_secs(30));
                    let new_info = query_daemon();
                    if new_info.state != prev_state {
                        set_tray_state(&tray_clone, &new_info.state);
                        prev_state = new_info.state;
                    }
                }
            });

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error running sky10-menu");
}
