use std::env;
use std::fs;
use std::io::{Read, Write};
use std::net::{SocketAddr, TcpStream};
use std::path::{Path, PathBuf};
use std::process::{Child, Command};
#[cfg(target_os = "windows")]
use std::process::Stdio;
use std::sync::Mutex;
use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::{
    menu::{AboutMetadata, MenuBuilder, MenuItemBuilder, SubmenuBuilder},
    AppHandle, Emitter, Manager,
};
use tauri_plugin_dialog::DialogExt;
use tauri_plugin_opener::OpenerExt;

const DEFAULT_PORT: u16 = 3000;
#[cfg(any(target_os = "macos", target_os = "linux"))]
const STARTUP_ENTRY_PATH_ENV: &str = "ENGINE_STARTUP_ENTRY_PATH";
#[cfg(target_os = "windows")]
const STARTUP_REG_PATH_ENV: &str = "ENGINE_STARTUP_REG_PATH";
#[cfg(target_os = "windows")]
const STARTUP_REG_NAME_ENV: &str = "ENGINE_STARTUP_REG_NAME";
#[cfg(target_os = "macos")]
const STARTUP_TEST_MODE_ENV: &str = "ENGINE_STARTUP_TEST_MODE";
const FRONTEND_MENU_EVENT: &str = "engine-shell-menu";
const CONTEXT_MENU_EVENT: &str = "engine-context-menu";

struct ServerProcess {
    child: Mutex<Option<Child>>,
    managed: bool,
}

struct ServerLaunch {
    child: Option<Child>,
    managed: bool,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct ServiceStatus {
    platform: String,
    installed: bool,
    running: bool,
    startup_target: String,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct EditorPreferences {
    font_family: String,
    font_size: u16,
    line_height: f32,
    tab_size: u8,
    word_wrap: bool,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct PathInspection {
    path: String,
    name: String,
    kind: String,
    parent_path: Option<String>,
}

#[derive(Serialize, Deserialize, Clone)]
#[serde(default)]
struct AppConfig {
    github_token: Option<String>,
    github_owner: Option<String>,
    github_repo: Option<String>,
    anthropic_api_key: Option<String>,
    openai_api_key: Option<String>,
    model: Option<String>,
    last_project_path: Option<String>,
    editor_font_family: String,
    editor_font_size: u16,
    editor_line_height: f32,
    editor_tab_size: u8,
    editor_word_wrap: bool,
}

enum CliAction {
    Background,
    InstallService,
    UninstallService,
    ServiceStatus,
}

impl Default for AppConfig {
    fn default() -> Self {
        Self {
            github_token: None,
            github_owner: None,
            github_repo: None,
            anthropic_api_key: None,
            openai_api_key: None,
            model: None,
            last_project_path: None,
            editor_font_family: default_editor_font_family(),
            editor_font_size: 13,
            editor_line_height: 1.6,
            editor_tab_size: 2,
            editor_word_wrap: false,
        }
    }
}

fn default_editor_font_family() -> String {
    "\"JetBrains Mono\", \"IBM Plex Mono\", Menlo, Monaco, monospace".to_string()
}

fn config_root() -> PathBuf {
    dirs::config_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("Engine")
}

fn config_path() -> PathBuf {
    config_root().join("config.json")
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

fn normalize_editor_preferences(settings: EditorPreferences) -> EditorPreferences {
    let font_family = match settings.font_family.as_str() {
        "\"JetBrains Mono\", \"IBM Plex Mono\", Menlo, Monaco, monospace"
        | "\"IBM Plex Mono\", \"JetBrains Mono\", Menlo, Monaco, monospace"
        | "\"Fira Code\", \"JetBrains Mono\", Menlo, Monaco, monospace"
        | "Menlo, Monaco, \"JetBrains Mono\", monospace" => settings.font_family,
        _ => default_editor_font_family(),
    };

    let line_height = settings.line_height.clamp(1.35, 2.05);
    let tab_size = match settings.tab_size {
        4 | 8 => settings.tab_size,
        _ => 2,
    };

    EditorPreferences {
        font_family,
        font_size: settings.font_size.clamp(11, 20),
        line_height: (line_height * 100.0).round() / 100.0,
        tab_size,
        word_wrap: settings.word_wrap,
    }
}

fn editor_preferences_from_config(cfg: &AppConfig) -> EditorPreferences {
    normalize_editor_preferences(EditorPreferences {
        font_family: cfg.editor_font_family.clone(),
        font_size: cfg.editor_font_size,
        line_height: cfg.editor_line_height,
        tab_size: cfg.editor_tab_size,
        word_wrap: cfg.editor_word_wrap,
    })
}

fn apply_editor_preferences(cfg: &mut AppConfig, settings: EditorPreferences) {
    let normalized = normalize_editor_preferences(settings);
    cfg.editor_font_family = normalized.font_family;
    cfg.editor_font_size = normalized.font_size;
    cfg.editor_line_height = normalized.line_height;
    cfg.editor_tab_size = normalized.tab_size;
    cfg.editor_word_wrap = normalized.word_wrap;
}

fn default_project_path() -> String {
    if let Some(home) = dirs::home_dir() {
        return home.to_string_lossy().to_string();
    }
    env::current_dir()
        .unwrap_or_else(|_| PathBuf::from("."))
        .to_string_lossy()
        .to_string()
}

fn project_path_for_server(cfg: &AppConfig) -> String {
    env::var("PROJECT_PATH")
        .ok()
        .filter(|path| !path.trim().is_empty())
        .or_else(|| cfg.last_project_path.clone())
        .unwrap_or_else(default_project_path)
}

#[cfg(target_os = "macos")]
fn logs_dir() -> PathBuf {
    dirs::home_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("Library")
        .join("Logs")
        .join("Engine")
}

fn server_running(port: u16) -> bool {
    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    let Ok(mut stream) = TcpStream::connect_timeout(&addr, Duration::from_millis(300)) else {
        return false;
    };

    let _ = stream.set_read_timeout(Some(Duration::from_millis(300)));
    let _ = stream.set_write_timeout(Some(Duration::from_millis(300)));
    let request = format!("GET /health HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\nConnection: close\r\n\r\n");
    if stream.write_all(request.as_bytes()).is_err() {
        return false;
    }

    let mut response = String::new();
    if stream.read_to_string(&mut response).is_err() {
        return false;
    }

    response.contains("\"status\":\"ok\"")
}

fn server_binary_names() -> [&'static str; 2] {
    if cfg!(target_os = "windows") {
        ["engine-server.exe", "engine-server"]
    } else {
        ["engine-server", "engine-server.exe"]
    }
}

fn push_server_binary_candidates(candidates: &mut Vec<PathBuf>, base: PathBuf) {
    for binary_name in server_binary_names() {
        candidates.push(base.join(binary_name));
    }
}

fn server_binary_path() -> PathBuf {
    let mut candidates = Vec::new();

    if cfg!(debug_assertions) {
        if let Ok(manifest_dir) = env::var("CARGO_MANIFEST_DIR") {
            let manifest_dir = PathBuf::from(manifest_dir);
            if let Some(repo_root) = manifest_dir
                .parent()
                .and_then(Path::parent)
                .and_then(Path::parent)
            {
                push_server_binary_candidates(
                    &mut candidates,
                    repo_root.join("packages").join("server-go"),
                );
                push_server_binary_candidates(&mut candidates, repo_root.join("server-go"));
            }
        }

        if let Ok(current_exe) = env::current_exe() {
            for ancestor in current_exe.ancestors().skip(1).take(7) {
                push_server_binary_candidates(
                    &mut candidates,
                    ancestor.join("packages").join("server-go"),
                );
                push_server_binary_candidates(&mut candidates, ancestor.join("server-go"));
            }
        }

        if let Ok(current_dir) = env::current_dir() {
            push_server_binary_candidates(
                &mut candidates,
                current_dir.join("packages").join("server-go"),
            );
            push_server_binary_candidates(&mut candidates, current_dir.join("server-go"));
            push_server_binary_candidates(
                &mut candidates,
                current_dir.join("..").join("..").join("server-go"),
            );
        }
    }

    push_server_binary_candidates(&mut candidates, PathBuf::from("resources"));

    if let Ok(current_exe) = env::current_exe() {
        if let Some(parent) = current_exe.parent() {
            push_server_binary_candidates(&mut candidates, parent.to_path_buf());
            push_server_binary_candidates(&mut candidates, parent.join("resources"));
            #[cfg(target_os = "macos")]
            push_server_binary_candidates(&mut candidates, parent.join("../Resources"));
        }
    }

    candidates
        .into_iter()
        .find(|path| path.exists())
        .unwrap_or_else(|| PathBuf::from("resources").join(server_binary_names()[0]))
}

fn configure_server_command(cmd: &mut Command, project_path: &str, cfg: &AppConfig) {
    cmd.env("PROJECT_PATH", project_path)
        .env("PORT", DEFAULT_PORT.to_string());

    if let Some(token) = &cfg.github_token {
        cmd.env("GITHUB_TOKEN", token);
    }
    if let Some(owner) = &cfg.github_owner {
        cmd.env("ENGINE_GITHUB_OWNER", owner);
    }
    if let Some(repo) = &cfg.github_repo {
        cmd.env("ENGINE_GITHUB_REPO", repo);
    }
    if let Some(key) = &cfg.anthropic_api_key {
        cmd.env("ANTHROPIC_API_KEY", key);
    }
    if let Some(key) = &cfg.openai_api_key {
        cmd.env("OPENAI_API_KEY", key);
    }
    if let Some(model) = &cfg.model {
        cmd.env("ENGINE_MODEL", model);
    }
}

fn start_go_server(project_path: &str, cfg: &AppConfig) -> ServerLaunch {
    if server_running(DEFAULT_PORT) {
        return ServerLaunch {
            child: None,
            managed: false,
        };
    }

    let server_bin = server_binary_path();
    if !server_bin.exists() {
        eprintln!("Engine server binary not found at {}", server_bin.display());
        return ServerLaunch {
            child: None,
            managed: false,
        };
    }

    let mut cmd = Command::new(&server_bin);
    configure_server_command(&mut cmd, project_path, cfg);

    match cmd.spawn() {
        Ok(child) => ServerLaunch {
            child: Some(child),
            managed: true,
        },
        Err(err) => {
            eprintln!("Failed to start Engine server: {err}");
            ServerLaunch {
                child: None,
                managed: false,
            }
        }
    }
}

fn run_background_service() {
    let cfg = read_config();
    let project_path = project_path_for_server(&cfg);
    let ServerLaunch { child, .. } = start_go_server(&project_path, &cfg);
    if let Some(mut managed_child) = child {
        let _ = managed_child.wait();
    }
}

fn cli_action() -> Option<CliAction> {
    env::args().skip(1).find_map(|arg| match arg.as_str() {
        "--background" => Some(CliAction::Background),
        "--install-service" => Some(CliAction::InstallService),
        "--uninstall-service" => Some(CliAction::UninstallService),
        "--service-status" => Some(CliAction::ServiceStatus),
        _ => None,
    })
}

#[cfg(target_os = "macos")]
fn startup_test_mode() -> bool {
    matches!(
        env::var(STARTUP_TEST_MODE_ENV).ok().as_deref(),
        Some("1" | "true" | "TRUE" | "yes" | "YES")
    )
}

#[cfg(any(target_os = "macos", target_os = "linux"))]
fn startup_entry_override() -> Option<PathBuf> {
    env::var(STARTUP_ENTRY_PATH_ENV)
        .ok()
        .map(|path| path.trim().to_string())
        .filter(|path| !path.is_empty())
        .map(PathBuf::from)
}

#[cfg(target_os = "macos")]
fn startup_entry_path() -> PathBuf {
    if let Some(path) = startup_entry_override() {
        return path;
    }

    dirs::home_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("Library")
        .join("LaunchAgents")
        .join("com.engine.app.plist")
}

#[cfg(target_os = "linux")]
fn startup_entry_path() -> PathBuf {
    if let Some(path) = startup_entry_override() {
        return path;
    }

    dirs::config_dir()
        .unwrap_or_else(|| PathBuf::from("."))
        .join("autostart")
        .join("engine-background.desktop")
}

#[cfg(target_os = "windows")]
fn startup_registry_path() -> String {
    env::var(STARTUP_REG_PATH_ENV)
        .ok()
        .map(|path| path.trim().to_string())
        .filter(|path| !path.is_empty())
        .unwrap_or_else(|| r"HKCU\Software\Microsoft\Windows\CurrentVersion\Run".to_string())
}

#[cfg(target_os = "windows")]
fn startup_registry_name() -> String {
    env::var(STARTUP_REG_NAME_ENV)
        .ok()
        .map(|name| name.trim().to_string())
        .filter(|name| !name.is_empty())
        .unwrap_or_else(|| "EngineBackground".to_string())
}

#[cfg(target_os = "macos")]
fn startup_service_target() -> String {
    startup_entry_path().display().to_string()
}

#[cfg(target_os = "linux")]
fn startup_service_target() -> String {
    startup_entry_path().display().to_string()
}

#[cfg(target_os = "windows")]
fn startup_service_target() -> String {
    format!(r"{}\{}", startup_registry_path(), startup_registry_name())
}

#[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
fn startup_service_target() -> String {
    String::from("unsupported")
}

#[cfg(target_os = "macos")]
fn startup_service_installed() -> bool {
    startup_entry_path().exists()
}

#[cfg(target_os = "linux")]
fn startup_service_installed() -> bool {
    startup_entry_path().exists()
}

#[cfg(target_os = "windows")]
fn startup_service_installed() -> bool {
    Command::new("reg")
        .args([
            "query",
            &startup_registry_path(),
            "/v",
            &startup_registry_name(),
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map(|status| status.success())
        .unwrap_or(false)
}

#[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
fn startup_service_installed() -> bool {
    false
}

#[cfg(target_os = "macos")]
fn install_startup_service(binary: &Path) -> Result<String, String> {
    let log_dir = logs_dir();
    fs::create_dir_all(&log_dir).map_err(|e| e.to_string())?;

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
  <true/>
  <key>StandardOutPath</key>
  <string>{stdout}</string>
  <key>StandardErrorPath</key>
  <string>{stderr}</string>
</dict>
</plist>"#,
        binary = binary.display(),
        stdout = log_dir.join("engine.log").display(),
        stderr = log_dir.join("engine-error.log").display(),
    );

    let plist_path = startup_entry_path();
    if let Some(parent) = plist_path.parent() {
        fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    fs::write(&plist_path, plist).map_err(|e| e.to_string())?;

    if startup_test_mode() {
        return Ok(String::from(
            "Engine background service test entry was written for macOS.",
        ));
    }

    let status = Command::new("launchctl")
        .args(["load", "-w", plist_path.to_str().unwrap_or("")])
        .status()
        .map_err(|e| e.to_string())?;

    if status.success() {
        Ok(String::from("Engine background service will start at login on macOS."))
    } else {
        Err(String::from("launchctl load failed — check ~/Library/Logs/Engine/engine-error.log"))
    }
}

#[cfg(target_os = "linux")]
fn install_startup_service(binary: &Path) -> Result<String, String> {
    let desktop_file = format!(
        "[Desktop Entry]\nType=Application\nVersion=1.0\nName=Engine Background Service\nComment=Start Engine background service at login\nExec=\"{}\" --background\nTerminal=false\nNoDisplay=true\nX-GNOME-Autostart-enabled=true\n",
        binary.display()
    );

    let autostart_path = startup_entry_path();
    if let Some(parent) = autostart_path.parent() {
        fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    fs::write(&autostart_path, desktop_file).map_err(|e| e.to_string())?;
    Ok(String::from("Engine background service will start at login on Linux."))
}

#[cfg(target_os = "windows")]
fn install_startup_service(binary: &Path) -> Result<String, String> {
    let command = format!("\"{}\" --background", binary.display());
    let status = Command::new("reg")
        .args([
            "add",
            &startup_registry_path(),
            "/v",
            &startup_registry_name(),
            "/t",
            "REG_SZ",
            "/d",
            &command,
            "/f",
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map_err(|e| e.to_string())?;

    if status.success() {
        Ok(String::from("Engine background service will start at login on Windows."))
    } else {
        Err(String::from("Failed to register Engine background service in Windows startup."))
    }
}

#[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
fn install_startup_service(_binary: &Path) -> Result<String, String> {
    Err(String::from("Startup/background service is not supported on this platform yet."))
}

#[cfg(target_os = "macos")]
fn uninstall_startup_service() -> Result<String, String> {
    let plist_path = startup_entry_path();
    if !plist_path.exists() {
        return Ok(String::from("Engine background service is not installed."));
    }

    if !startup_test_mode() {
        let _ = Command::new("launchctl")
            .args(["unload", "-w", plist_path.to_str().unwrap_or("")])
            .status();
    }

    fs::remove_file(&plist_path).map_err(|e| e.to_string())?;
    Ok(String::from("Engine background service removed from macOS login items."))
}

#[cfg(target_os = "linux")]
fn uninstall_startup_service() -> Result<String, String> {
    let autostart_path = startup_entry_path();
    if !autostart_path.exists() {
        return Ok(String::from("Engine background service is not installed."));
    }

    fs::remove_file(&autostart_path).map_err(|e| e.to_string())?;
    Ok(String::from("Engine background service removed from Linux login startup."))
}

#[cfg(target_os = "windows")]
fn uninstall_startup_service() -> Result<String, String> {
    let status = Command::new("reg")
        .args([
            "delete",
            &startup_registry_path(),
            "/v",
            &startup_registry_name(),
            "/f",
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map_err(|e| e.to_string())?;

    if status.success() {
        Ok(String::from("Engine background service removed from Windows startup."))
    } else {
        Err(String::from("Failed to remove Engine background service from Windows startup."))
    }
}

#[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
fn uninstall_startup_service() -> Result<String, String> {
    Err(String::from("Startup/background service is not supported on this platform yet."))
}

fn platform_name() -> String {
    env::consts::OS.to_string()
}

fn service_status() -> ServiceStatus {
    ServiceStatus {
        platform: platform_name(),
        installed: startup_service_installed(),
        running: server_running(DEFAULT_PORT),
        startup_target: startup_service_target(),
    }
}

fn install_agent_service_cli() -> Result<String, String> {
    let binary = env::current_exe().map_err(|e| e.to_string())?;
    install_startup_service(&binary)
}

// ── Tauri commands ────────────────────────────────────────────────────────────

#[tauri::command]
fn get_project_path() -> String {
    env::var("PROJECT_PATH")
        .ok()
        .filter(|path| !path.trim().is_empty())
        .unwrap_or_else(|| read_config().last_project_path.unwrap_or_default())
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
fn get_github_owner() -> Option<String> {
    read_config().github_owner
}

#[tauri::command]
fn set_github_owner(owner: String) -> bool {
    let mut cfg = read_config();
    cfg.github_owner = if owner.trim().is_empty() {
        None
    } else {
        Some(owner.trim().to_string())
    };
    write_config(&cfg)
}

#[tauri::command]
fn get_github_repo() -> Option<String> {
    read_config().github_repo
}

#[tauri::command]
fn set_github_repo(repo: String) -> bool {
    let mut cfg = read_config();
    cfg.github_repo = if repo.trim().is_empty() {
        None
    } else {
        Some(repo.trim().to_string())
    };
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
fn get_editor_preferences() -> EditorPreferences {
    editor_preferences_from_config(&read_config())
}

#[tauri::command]
fn set_editor_preferences(settings: EditorPreferences) -> bool {
    let mut cfg = read_config();
    apply_editor_preferences(&mut cfg, settings);
    write_config(&cfg)
}

#[tauri::command]
fn inspect_path(path: String) -> Result<PathInspection, String> {
    let trimmed_path = path.trim();
    if trimmed_path.is_empty() {
        return Err("path must not be empty".to_string());
    }

    let resolved = PathBuf::from(trimmed_path);
    let metadata = fs::metadata(&resolved).map_err(|error| error.to_string())?;
    let kind = if metadata.is_dir() {
        "directory".to_string()
    } else {
        "file".to_string()
    };

    Ok(PathInspection {
        path: resolved.to_string_lossy().to_string(),
        name: resolved
            .file_name()
            .map(|name| name.to_string_lossy().to_string())
            .unwrap_or_else(|| resolved.to_string_lossy().to_string()),
        kind,
        parent_path: resolved.parent().map(|parent| parent.to_string_lossy().to_string()),
    })
}

#[tauri::command]
async fn open_folder_dialog(app: AppHandle) -> Option<String> {
    let cfg = read_config();
    let mut dialog = app.dialog().file().set_title("Open workspace");
    if let Some(dir) = cfg
        .last_project_path
        .as_deref()
        .filter(|path| !path.trim().is_empty())
        .map(PathBuf::from)
        .or_else(|| app.path().home_dir().ok())
    {
        dialog = dialog.set_directory(dir);
    }

    let folder = dialog.blocking_pick_folder().map(|path| {
        path.as_path()
            .map(|resolved| resolved.to_string_lossy().to_string())
            .unwrap_or_else(|| path.to_string())
    });

    if let Some(path) = folder.as_ref() {
        let mut next_cfg = read_config();
        next_cfg.last_project_path = Some(path.clone());
        let _ = write_config(&next_cfg);
    }

    folder
}

#[tauri::command]
async fn open_file_dialog(app: AppHandle) -> Option<String> {
    let cfg = read_config();
    let mut dialog = app.dialog().file().set_title("Open file");
    if let Some(dir) = cfg
        .last_project_path
        .as_deref()
        .filter(|path| !path.trim().is_empty())
        .map(PathBuf::from)
        .or_else(|| app.path().home_dir().ok())
    {
        dialog = dialog.set_directory(dir);
    }

    dialog.blocking_pick_file().map(|path| {
        path.as_path()
            .map(|resolved| resolved.to_string_lossy().to_string())
            .unwrap_or_else(|| path.to_string())
    })
}

#[tauri::command]
fn set_last_project_path(path: String) -> bool {
    let mut cfg = read_config();
    cfg.last_project_path = if path.trim().is_empty() {
        None
    } else {
        Some(path)
    };
    write_config(&cfg)
}

#[tauri::command]
fn window_minimize(app: AppHandle) -> Result<(), String> {
    app.get_webview_window("main")
        .ok_or_else(|| "main window not found".to_string())?
        .minimize()
        .map_err(|e| e.to_string())
}

#[tauri::command]
fn window_toggle_maximize(app: AppHandle) -> Result<(), String> {
    let window = app
        .get_webview_window("main")
        .ok_or_else(|| "main window not found".to_string())?;

    if window.is_maximized().map_err(|e| e.to_string())? {
        window.unmaximize().map_err(|e| e.to_string())
    } else {
        window.maximize().map_err(|e| e.to_string())
    }
}

#[tauri::command]
fn window_toggle_fullscreen(app: AppHandle) -> Result<(), String> {
    let window = app
        .get_webview_window("main")
        .ok_or_else(|| "main window not found".to_string())?;
    
    let is_fs = window.is_fullscreen().map_err(|e| e.to_string())?;
    window.set_fullscreen(!is_fs).map_err(|e| e.to_string())
}

#[tauri::command]
fn window_close(app: AppHandle) -> Result<(), String> {
    app.get_webview_window("main")
        .ok_or_else(|| "main window not found".to_string())?
        .close()
        .map_err(|e| e.to_string())
}

#[tauri::command]
fn window_start_drag(app: AppHandle) -> Result<(), String> {
    app.get_webview_window("main")
        .ok_or_else(|| "main window not found".to_string())?
        .start_dragging()
        .map_err(|e| e.to_string())
}

#[tauri::command]
async fn open_external(url: String, app: AppHandle) -> Result<(), String> {
    app.opener()
        .open_url(&url, None::<&str>)
        .map_err(|e| e.to_string())
}

#[tauri::command]
fn install_agent_service() -> Result<String, String> {
    let binary = env::current_exe().map_err(|e| e.to_string())?;
    install_startup_service(&binary)
}

#[tauri::command]
fn uninstall_agent_service() -> Result<String, String> {
    uninstall_startup_service()
}

#[tauri::command]
fn agent_service_status() -> ServiceStatus {
    service_status()
}

#[tauri::command]
fn show_context_menu(app: AppHandle, x: i32, y: i32, items: Vec<(String, String)>) -> Result<(), String> {
    use tauri::Position;
    
    let window = app
        .get_webview_window("main")
        .ok_or_else(|| "main window not found".to_string())?;

    let mut menu = MenuBuilder::new(&app);
    
    for (label, id) in items {
        let item = MenuItemBuilder::new(&label).id(&id).build(&app).map_err(|e| e.to_string())?;
        menu = menu.item(&item);
    }

    let context_menu = menu.build().map_err(|e| e.to_string())?;
    let pos = Position::Logical((x as f64, y as f64).into());
    window.popup_menu_at(&context_menu, pos).map_err(|e| e.to_string())
}

// ── App entry point ───────────────────────────────────────────────────────────

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    match cli_action() {
        Some(CliAction::Background) => {
            run_background_service();
            return;
        }
        Some(CliAction::InstallService) => match install_agent_service_cli() {
            Ok(message) => {
                println!("{message}");
                return;
            }
            Err(error) => {
                eprintln!("{error}");
                std::process::exit(1);
            }
        },
        Some(CliAction::UninstallService) => match uninstall_startup_service() {
            Ok(message) => {
                println!("{message}");
                return;
            }
            Err(error) => {
                eprintln!("{error}");
                std::process::exit(1);
            }
        },
        Some(CliAction::ServiceStatus) => {
            println!(
                "{}",
                serde_json::to_string(&service_status()).unwrap_or_else(|_| "{}".to_string())
            );
            return;
        }
        None => {}
    }

    let cfg = read_config();
    let project_path = project_path_for_server(&cfg);
    let server = start_go_server(&project_path, &cfg);

    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_process::init())
        .setup(|app| {
            let open_folder = MenuItemBuilder::new("Open Folder…")
                .id("open-folder")
                .accelerator("CmdOrCtrl+O")
                .build(app)?;
            let open_file = MenuItemBuilder::new("Open File…")
                .id("open-file")
                .accelerator("CmdOrCtrl+Shift+O")
                .build(app)?;
            let build_workspace = MenuItemBuilder::new("Build Workspace")
                .id("build-workspace")
                .build(app)?;
            let run_workspace = MenuItemBuilder::new("Run Workspace")
                .id("run-workspace")
                .accelerator("CmdOrCtrl+Shift+B")
                .build(app)?;
            let save_file = MenuItemBuilder::new("Save")
                .id("save-file")
                .accelerator("CmdOrCtrl+S")
                .build(app)?;
            let save_all_files = MenuItemBuilder::new("Save All")
                .id("save-all-files")
                .accelerator("CmdOrCtrl+Shift+S")
                .build(app)?;
            let open_preferences = MenuItemBuilder::new("Preferences…")
                .id("open-preferences")
                .accelerator("CmdOrCtrl+,")
                .build(app)?;
            let toggle_sidebar = MenuItemBuilder::new("Toggle Sidebar")
                .id("toggle-sidebar")
                .accelerator("CmdOrCtrl+B")
                .build(app)?;
            let toggle_terminal = MenuItemBuilder::new("Toggle Terminal")
                .id("toggle-terminal")
                .accelerator("CmdOrCtrl+J")
                .build(app)?;
            let focus_chat = MenuItemBuilder::new("Focus Chat")
                .id("focus-chat")
                .accelerator("CmdOrCtrl+L")
                .build(app)?;
            let open_project_page = MenuItemBuilder::new("Project Home")
                .id("open-project-page")
                .build(app)?;

            let app_submenu = SubmenuBuilder::new(app, "Engine")
                .about(Some(AboutMetadata {
                    name: Some("Engine".to_string()),
                    version: Some(env!("CARGO_PKG_VERSION").to_string()),
                    comments: Some("AI-native code editor".to_string()),
                    ..Default::default()
                }))
                .separator()
                .item(&open_preferences)
                .separator()
                .quit()
                .build()?;

            let file_submenu = SubmenuBuilder::new(app, "File")
                .item(&open_folder)
                .item(&open_file)
                .separator()
                .item(&build_workspace)
                .item(&run_workspace)
                .separator()
                .item(&save_file)
                .item(&save_all_files)
                .build()?;

            let edit_submenu = SubmenuBuilder::new(app, "Edit")
                .undo()
                .redo()
                .separator()
                .cut()
                .copy()
                .paste()
                .select_all()
                .build()?;

            let view_submenu = SubmenuBuilder::new(app, "View")
                .item(&toggle_sidebar)
                .item(&toggle_terminal)
                .item(&focus_chat)
                .build()?;

            let help_submenu = SubmenuBuilder::new(app, "Help")
                .item(&open_project_page)
                .build()?;

            let menu = MenuBuilder::new(app)
                .items(&[
                    &app_submenu,
                    &file_submenu,
                    &edit_submenu,
                    &view_submenu,
                    &help_submenu,
                ])
                .build()?;

            app.set_menu(menu)?;
            Ok(())
        })
        .manage(ServerProcess {
            child: Mutex::new(server.child),
            managed: server.managed,
        })
        .invoke_handler(tauri::generate_handler![
            get_project_path,
            get_github_token,
            set_github_token,
            get_github_owner,
            set_github_owner,
            get_github_repo,
            set_github_repo,
            get_anthropic_key,
            set_anthropic_key,
            get_openai_key,
            set_openai_key,
            get_model,
            set_model,
            get_editor_preferences,
            set_editor_preferences,
            inspect_path,
            set_last_project_path,
            install_agent_service,
            uninstall_agent_service,
            agent_service_status,
            open_external,
            open_folder_dialog,
            open_file_dialog,
            window_minimize,
            window_toggle_maximize,
            window_toggle_fullscreen,
            window_close,
            window_start_drag,
            show_context_menu,
        ])
        .on_menu_event(|app, event| match event.id().0.as_str() {
            "open-folder" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "open-folder");
            }
            "open-file" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "open-file");
            }
            "build-workspace" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "build-workspace");
            }
            "run-workspace" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "run-workspace");
            }
            "save-file" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "save-file");
            }
            "save-all-files" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "save-all-files");
            }
            "open-preferences" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "open-preferences");
            }
            "toggle-sidebar" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "toggle-sidebar");
            }
            "toggle-terminal" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "toggle-terminal");
            }
            "focus-chat" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "focus-chat");
            }
            "open-project-page" => {
                let _ = app.emit(FRONTEND_MENU_EVENT, "open-project-page");
            }
            "new-file" => {
                let _ = app.emit(CONTEXT_MENU_EVENT, "new-file");
            }
            "new-folder" => {
                let _ = app.emit(CONTEXT_MENU_EVENT, "new-folder");
            }
            "group-folders" => {
                let _ = app.emit(CONTEXT_MENU_EVENT, "group-folders");
            }
            _ => {}
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::Destroyed = event {
                if let Some(state) = window.try_state::<ServerProcess>() {
                    if state.managed {
                        if let Ok(mut guard) = state.child.lock() {
                            if let Some(mut child) = guard.take() {
                                let _ = child.kill();
                            }
                        }
                    }
                }
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
