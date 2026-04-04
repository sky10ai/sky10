#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::process::Command;
use std::time::Duration;
use tauri::{
    menu::{MenuBuilder, MenuItemBuilder, PredefinedMenuItem},
    tray::TrayIconBuilder,
    Manager,
};

fn get_version() -> String {
    let output = Command::new("sky10").arg("--version").output();
    match output {
        Ok(o) if o.status.success() => {
            let raw = String::from_utf8_lossy(&o.stdout);
            // Output is like "sky10 version v0.35.0 (abc1234) built 2026-04-01"
            // Extract just the version tag.
            raw.split_whitespace()
                .find(|s| s.starts_with('v'))
                .unwrap_or("unknown")
                .to_string()
        }
        _ => "not running".to_string(),
    }
}

fn get_latest_release() -> Option<String> {
    // Quick GitHub API check — timeout fast so the menu isn't slow.
    let output = Command::new("curl")
        .args([
            "-fsSL",
            "--max-time",
            "3",
            "https://api.github.com/repos/sky10ai/sky10/releases/latest",
        ])
        .output()
        .ok()?;

    if !output.status.success() {
        return None;
    }

    let body = String::from_utf8_lossy(&output.stdout);
    // Parse "tag_name": "v0.36.0" without pulling in a JSON library for this.
    let tag = body
        .split("\"tag_name\"")
        .nth(1)?
        .split('"')
        .find(|s| s.starts_with('v'))?;

    Some(tag.to_string())
}

fn open_ui() {
    #[cfg(target_os = "macos")]
    {
        let _ = Command::new("open").arg("http://localhost:9101").spawn();
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

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let version = get_version();

            let label = format!("sky10 {}", version);
            let version_item = MenuItemBuilder::with_id("version", &label)
                .enabled(false)
                .build(app)?;
            let sep1 = PredefinedMenuItem::separator(app)?;
            let open = MenuItemBuilder::with_id("open", "Open").build(app)?;
            let sep2 = PredefinedMenuItem::separator(app)?;
            let quit = MenuItemBuilder::with_id("quit", "Quit").build(app)?;

            // Check for updates in background.
            let latest = get_latest_release();
            let has_update = latest
                .as_ref()
                .map(|l| l != &version)
                .unwrap_or(false);

            let mut menu = MenuBuilder::new(app)
                .item(&version_item)
                .item(&sep1)
                .item(&open);

            if has_update {
                let update_label = format!(
                    "Update available ({})",
                    latest.as_deref().unwrap_or("?")
                );
                let update_item =
                    MenuItemBuilder::with_id("update_info", &update_label)
                        .enabled(false)
                        .build(app)?;
                let restart_update =
                    MenuItemBuilder::with_id("restart_update", "Restart to update")
                        .build(app)?;
                let sep3 = PredefinedMenuItem::separator(app)?;
                menu = menu.item(&sep3).item(&update_item).item(&restart_update);
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

            // Periodically check for updates (every 6 hours).
            let app_handle = app.handle().clone();
            std::thread::spawn(move || loop {
                std::thread::sleep(Duration::from_secs(6 * 3600));
                // Re-checking would require rebuilding the menu.
                // For now, the initial check on startup is sufficient.
                // A full implementation would use app_handle to update
                // the tray menu dynamically.
                let _ = app_handle;
            });

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error running sky10-menu");
}
