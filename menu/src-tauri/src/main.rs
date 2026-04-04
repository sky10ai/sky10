#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::process::Command;
use tauri::{
    menu::{MenuBuilder, MenuItemBuilder},
    tray::TrayIconBuilder,
    Manager,
};

fn open_web_ui() {
    // Query the daemon for the HTTP address, fall back to default port.
    let port = get_daemon_port().unwrap_or_else(|| "9101".to_string());
    let url = format!("http://localhost:{}", port);

    #[cfg(target_os = "macos")]
    { let _ = Command::new("open").arg(&url).spawn(); }

    #[cfg(target_os = "linux")]
    { let _ = Command::new("xdg-open").arg(&url).spawn(); }

    #[cfg(target_os = "windows")]
    { let _ = Command::new("cmd").args(["/C", "start", &url]).spawn(); }
}

fn get_daemon_port() -> Option<String> {
    let output = Command::new("sky10")
        .args(["ui", "open", "--print-only"])
        .output()
        .ok()?;
    if output.status.success() {
        let url = String::from_utf8_lossy(&output.stdout);
        // Extract port from http://localhost:XXXX
        url.trim().rsplit(':').next().map(|s| s.to_string())
    } else {
        None
    }
}

fn restart_daemon() {
    let _ = Command::new("sky10")
        .args(["daemon", "restart"])
        .spawn();
}

fn stop_daemon() {
    let _ = Command::new("sky10")
        .args(["daemon", "stop"])
        .spawn();
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let open = MenuItemBuilder::with_id("open", "Open Web UI").build(app)?;
            let restart = MenuItemBuilder::with_id("restart", "Restart Daemon").build(app)?;
            let separator = tauri::menu::PredefinedMenuItem::separator(app)?;
            let quit = MenuItemBuilder::with_id("quit", "Quit").build(app)?;

            let menu = MenuBuilder::new(app)
                .item(&open)
                .item(&restart)
                .item(&separator)
                .item(&quit)
                .build()?;

            TrayIconBuilder::new()
                .icon(app.default_window_icon().unwrap().clone())
                .menu(&menu)
                .tooltip("Sky10")
                .on_menu_event(|app, event| match event.id().as_ref() {
                    "open" => open_web_ui(),
                    "restart" => restart_daemon(),
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
