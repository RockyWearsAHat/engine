use std::fs;
use std::path::PathBuf;
use std::process::{Child, Command};
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager};
use tauri_plugin_opener::OpenerExt;

struct ServerProcess(Mutex<Option<Child>>);

#[derive(Serialize, Deserialize)]
struct AppConfig {
    github_token: Option<String>,
}

fn config_path() -> PathBuf {
    dirs::config_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("MyEditor")
        .join("config.json")
}

fn read_config() -> AppConfig {
    let path = config_path();
    if let Ok(data) = fs::read_to_string(&path) {
        serde_json::from_str(&data).unwrap_or(AppConfig { github_token: None })
    } else {
        AppConfig { github_token: None }
    }
}

fn write_config(cfg: &AppConfig) -> bool {
    let path = config_path();
    if let Some(parent) = path.parent() {
        let _ = fs::create_dir_all(parent);
    }
    if let Ok(json) = serde_json::to_string_pretty(cfg) {
        fs::write(path, json).is_ok()
    } else {
        false
    }
}

// ── Tauri commands (replace the 4 Electron IPC channels) ─────────────────────

#[tauri::command]
fn get_project_path(app: AppHandle) -> String {
    std::env::var("PROJECT_PATH").unwrap_or_else(|_| {
        app.path().home_dir()
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_else(|_| String::from("/"))
    })
}

#[tauri::command]
fn get_github_token() -> Option<String> {
    read_config().github_token
}

#[tauri::command]
fn set_github_token(token: String) -> bool {
    let mut cfg = read_config();
    cfg.github_token = if token.is_empty() { None } else { Some(token) };
    write_config(&cfg)
}

#[tauri::command]
async fn open_external(url: String, app: AppHandle) -> Result<(), String> {
    app.opener().open_url(&url, None::<&str>).map_err(|e| e.to_string())
}

// ── Server sidecar management ─────────────────────────────────────────────────

fn start_go_server(project_path: &str) -> Option<Child> {
    // In development the Go binary is built separately; in production Tauri
    // bundles it as a sidecar at resources/server-go (configured in tauri.conf.json).
    let server_bin = if cfg!(debug_assertions) {
        // Dev: use the built binary relative to workspace root
        let workspace = std::env::var("CARGO_MANIFEST_DIR").unwrap_or_default();
        PathBuf::from(&workspace)
            .parent().unwrap_or(&PathBuf::from("."))
            .parent().unwrap_or(&PathBuf::from("."))
            .join("server-go")
            .join("myeditor-server")
    } else {
        // Production: Tauri places sidecars in the resources directory
        PathBuf::from("resources").join("myeditor-server")
    };

    Command::new(&server_bin)
        .env("PROJECT_PATH", project_path)
        .env("PORT", "3000")
        .spawn()
        .ok()
}

// ── App entry point ───────────────────────────────────────────────────────────

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let project_path = std::env::var("PROJECT_PATH")
        .unwrap_or_else(|_| dirs::home_dir()
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_else(|| String::from("/")));

    let server = start_go_server(&project_path);

    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_process::init())
        .manage(ServerProcess(Mutex::new(server)))
        .invoke_handler(tauri::generate_handler![
            get_project_path,
            get_github_token,
            set_github_token,
            open_external,
        ])
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::Destroyed = event {
                // Kill Go server when the last window closes
                if let Some(state) = window.try_state::<ServerProcess>() {
                    if let Ok(mut guard) = state.0.lock() {
                        if let Some(mut child) = guard.take() {
                            let _ = child.kill();
                        }
                    }
                }
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
