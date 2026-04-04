// Tauri requires a thin main.rs that delegates to lib.rs.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    engine_lib::run();
}
