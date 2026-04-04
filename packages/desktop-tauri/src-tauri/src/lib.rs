use std::fs;
use std::path::PathBuf;
use std::process::{Child, Command};
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Manager};
use tauri_plugin_dialog::DialogExt;
use tauri_plugin_opener::OpenerExt;

struct ServerProcess(Mutex<Option<Child>>);

#[derive(Serialize, Deserialize, Default)]
struct AppConfig {
    github_token: Option<String>,
    anthropic_api_key: Option<String>,
    openai_api_key: Option<String>,
    model: Option<String>,
}

fn config_path() -> PathBuf {
    dirs::config_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("Engine")
        .join("config.json")
}

fn read_config() -> AppConfig {
    let path = config_path();
    if let Ok(data) = fs::read_to_string(&path) {
        serde_json::from_str(&data).unwrap_or_default()
    } else {
        AppConfig::default()
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

// ── LaunchAgent plist path (macOS) ────────────────────────────────────────────

fn launchagent_plist_path() -> PathBuf {
    dirs::home_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("Library")
        .join("LaunchAgents")
        .join("com.engine.app.plist")
}

// ── Tauri commands ────────────────────────────────────────────────────────────

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
fn get_anthropic_key() -> Option<String> {
    read_config().anthropic_api_key
}

#[tauri::command]
fn set_anthropic_key(key: String) -> bool {
    let mut cfg = read_config();
    cfg.anthropic_api_key = if key.is_empty() { None } else { Some(key) };
    write_config(&cfg)
}

#[tauri::command]
fn get_openai_key() -> Option<String> {
    read_config().openai_api_key
}

#[tauri::command]
fn set_openai_key(key: String) -> bool {
    let mut cfg = read_config();
    cfg.openai_api_key = if key.is_empty() { None } else { Some(key) };
    write_config(&cfg)
}

#[tauri::command]
fn get_model() -> Option<String> {
    read_config().model
}

#[tauri::command]
fn set_model(model: String) -> bool {
    let mut cfg = read_config();
    cfg.model = if model.is_empty() { None } else { Some(model) };
    write_config(&cfg)
}

#[tauri::command]
async fn open_folder_dialog(app: AppHandle) -> Option<String> {
    app.dialog()
        .file()
        .blocking_pick_folder()
        .map(|p| p.to_string())
}

#[tauri::command]
async fn open_external(url: String, app: AppHandle) -> Result<(), String> {
    app.opener().open_url(&url, None::<&str>).map_err(|e| e.to_string())
}

// ── Agent service (macOS launchd) ─────────────────────────────────────────────

#[tauri::command]
fn install_agent_service(app: AppHandle) -> Result<String, String> {
    let binary = std::env::current_exe()
        .map_err(|e| e.to_string())?;
    let home = app.path().home_dir()
        .map(|p| p.to_string_lossy().to_string())
        .unwrap_or_else(|_| String::from("/tmp"));
    let log_dir = format!("{}/Library/Logs/Engine", home);
    let _ = fs::create_dir_all(&log_dir);

    let plist = format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.engine.app</string>
  <key>ProgramArguments</key>
  <array>
    <string>{binary}</string>
    <string>--background</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <false/>
  <key>StandardOutPath</key>
  <string>{log_dir}/engine.log</string>
  <key>StandardErrorPath</key>
  <string>{log_dir}/engine-error.log</string>
</dict>
</plist>"#,
        binary = binary.display(),
        log_dir = log_dir,
    );

    let plist_path = launchagent_plist_path();
    if let Some(parent) = plist_path.parent() {
        fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    fs::write(&plist_path, plist).map_err(|e| e.to_string())?;

    let status = Command::new("launchctl")
        .args(["load", "-w", plist_path.to_str().unwrap_or("")])
        .status()
        .map_err(|e| e.to_string())?;

    if status.success() {
        Ok("Engine Agent Service installed and enabled at login.".to_string())
    } else {
        Err("launchctl load failed — check ~/Library/Logs/Engine/engine-error.log".to_string())
    }
}

#[tauri::command]
fn uninstall_agent_service() -> Result<String, String> {
    let plist_path = launchagent_plist_path();
    if !plist_path.exists() {
        return Ok("Agent service is not installed.".to_string());
    }

    let _ = Command::new("launchctl")
        .args(["unload", "-w", plist_path.to_str().unwrap_or("")])
        .status();

    fs::remove_file(&plist_path).map_err(|e| e.to_string())?;
    Ok("Engine Agent Service removed.".to_string())
}

#[tauri::command]
fn agent_service_status() -> String {
    if launchagent_plist_path().exists() {
        "installed".to_string()
    } else {
        "not_installed".to_string()
    }
}

// ── Server sidecar management ─────────────────────────────────────────────────

fn start_go_server(project_path: &str, cfg: &AppConfig) -> Option<Child> {
    let server_bin = if cfg!(debug_assertions) {
        let workspace = std::env::var("CARGO_MANIFEST_DIR").unwrap_or_default();
        PathBuf::from(&workspace)
            .parent().unwrap_or(&PathBuf::from("."))
            .parent().unwrap_or(&PathBuf::from("."))
            .join("server-go")
            .join("engine-server")
    } else {
        PathBuf::from("resources").join("engine-server")
    };

    let mut cmd = Command::new(&server_bin);
    cmd.env("PROJECT_PATH", project_path)
       .env("PORT", "3000");

    if let Some(key) = &cfg.anthropic_api_key {
        cmd.env("ANTHROPIC_API_KEY", key);
    }
    if let Some(key) = &cfg.openai_api_key {
        cmd.env("OPENAI_API_KEY", key);
    }
    if let Some(model) = &cfg.model {
        cmd.env("ENGINE_MODEL", model);
    }

    cmd.spawn().ok()
}

// ── App entry point ───────────────────────────────────────────────────────────

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let project_path = std::env::var("PROJECT_PATH")
        .unwrap_or_else(|_| dirs::home_dir()
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_else(|| String::from("/")));

    let cfg = read_config();
    let server = start_go_server(&project_path, &cfg);

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
            get_anthropic_key,
            set_anthropic_key,
            get_openai_key,
            set_openai_key,
            get_model,
            set_model,
            install_agent_service,
            uninstall_agent_service,
            agent_service_status,
            open_external,
            open_folder_dialog,
        ])
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::Destroyed = event {
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
