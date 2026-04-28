use std::env;
use std::path::PathBuf;

use serde::{Deserialize, Serialize};

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct EditorPreferences {
    pub(crate) font_family: String,
    pub(crate) font_size: u16,
    pub(crate) line_height: f32,
    pub(crate) tab_size: u8,
    pub(crate) word_wrap: bool,
}

#[derive(Serialize, Deserialize, Clone)]
#[serde(default)]
pub(crate) struct AppConfig {
    pub(crate) github_token: Option<String>,
    pub(crate) github_owner: Option<String>,
    pub(crate) github_repo: Option<String>,
    pub(crate) anthropic_api_key: Option<String>,
    pub(crate) openai_api_key: Option<String>,
    pub(crate) model_provider: Option<String>,
    pub(crate) ollama_base_url: Option<String>,
    pub(crate) model: Option<String>,
    pub(crate) clones_dir: Option<String>,
    pub(crate) last_project_path: Option<String>,
    pub(crate) local_server_token: Option<String>,
    pub(crate) editor_font_family: String,
    pub(crate) editor_font_size: u16,
    pub(crate) editor_line_height: f32,
    pub(crate) editor_tab_size: u8,
    pub(crate) editor_word_wrap: bool,
    #[serde(default)]
    pub(crate) active_team: Option<String>,
}

impl Default for AppConfig {
    fn default() -> Self {
        Self {
            github_token: None,
            github_owner: None,
            github_repo: None,
            anthropic_api_key: None,
            openai_api_key: None,
            model_provider: Some("ollama".to_string()),
            ollama_base_url: None,
            model: None,
            clones_dir: None,
            last_project_path: None,
            local_server_token: None,
            editor_font_family: default_editor_font_family(),
            editor_font_size: 13,
            editor_line_height: 1.6,
            editor_tab_size: 2,
            editor_word_wrap: false,
            active_team: None,
        }
    }
}

pub(crate) fn default_editor_font_family() -> String {
    "\"JetBrains Mono\", \"IBM Plex Mono\", Menlo, Monaco, monospace".to_string()
}

pub(crate) fn normalize_editor_preferences(settings: EditorPreferences) -> EditorPreferences {
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

pub(crate) fn editor_preferences_from_config(cfg: &AppConfig) -> EditorPreferences {
    normalize_editor_preferences(EditorPreferences {
        font_family: cfg.editor_font_family.clone(),
        font_size: cfg.editor_font_size,
        line_height: cfg.editor_line_height,
        tab_size: cfg.editor_tab_size,
        word_wrap: cfg.editor_word_wrap,
    })
}

pub(crate) fn apply_editor_preferences(cfg: &mut AppConfig, settings: EditorPreferences) {
    let normalized = normalize_editor_preferences(settings);
    cfg.editor_font_family = normalized.font_family;
    cfg.editor_font_size = normalized.font_size;
    cfg.editor_line_height = normalized.line_height;
    cfg.editor_tab_size = normalized.tab_size;
    cfg.editor_word_wrap = normalized.word_wrap;
}

fn default_project_path_from(home: Option<PathBuf>, current_dir: Option<PathBuf>) -> String {
    if let Some(home) = home {
        return home.to_string_lossy().to_string();
    }

    current_dir
        .unwrap_or_else(|| PathBuf::from("."))
        .to_string_lossy()
        .to_string()
}

pub(crate) fn default_project_path() -> String {
    default_project_path_from(dirs::home_dir(), env::current_dir().ok())
}

fn project_path_for_server_with_env(
    cfg: &AppConfig,
    project_path_env: Option<String>,
) -> String {
    project_path_env
        .filter(|path| !path.trim().is_empty())
        .or_else(|| cfg.last_project_path.clone())
        .unwrap_or_else(default_project_path)
}

pub(crate) fn project_path_for_server(cfg: &AppConfig) -> String {
    project_path_for_server_with_env(cfg, env::var("PROJECT_PATH").ok())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_prefs(
        font_family: &str,
        font_size: u16,
        line_height: f32,
        tab_size: u8,
        word_wrap: bool,
    ) -> EditorPreferences {
        EditorPreferences {
            font_family: font_family.to_string(),
            font_size,
            line_height,
            tab_size,
            word_wrap,
        }
    }

    #[test]
    fn default_font_family_contains_jetbrains_mono() {
        let result = default_editor_font_family();
        assert!(result.contains("JetBrains Mono"), "must include JetBrains Mono");
        assert!(result.contains("monospace"), "must include monospace fallback");
    }

    #[test]
    fn normalize_known_font_families_pass_through() {
        let known = [
            "\"JetBrains Mono\", \"IBM Plex Mono\", Menlo, Monaco, monospace",
            "\"IBM Plex Mono\", \"JetBrains Mono\", Menlo, Monaco, monospace",
            "\"Fira Code\", \"JetBrains Mono\", Menlo, Monaco, monospace",
            "Menlo, Monaco, \"JetBrains Mono\", monospace",
        ];
        for family in known {
            let result = normalize_editor_preferences(make_prefs(family, 13, 1.6, 2, false));
            assert_eq!(result.font_family, family, "known family should pass through: {family}");
        }
    }

    #[test]
    fn normalize_unknown_font_family_uses_default() {
        let result = normalize_editor_preferences(make_prefs("Comic Sans", 13, 1.6, 2, false));
        assert_eq!(result.font_family, default_editor_font_family());
    }

    #[test]
    fn normalize_line_height_clamped_to_min() {
        let result = normalize_editor_preferences(make_prefs(&default_editor_font_family(), 13, 0.5, 2, false));
        assert_eq!(result.line_height, 1.35);
    }

    #[test]
    fn normalize_line_height_clamped_to_max() {
        let result = normalize_editor_preferences(make_prefs(&default_editor_font_family(), 13, 3.0, 2, false));
        assert_eq!(result.line_height, 2.05);
    }

    #[test]
    fn normalize_line_height_within_range_unchanged() {
        let result = normalize_editor_preferences(make_prefs(&default_editor_font_family(), 13, 1.6, 2, false));
        assert!((result.line_height - 1.6_f32).abs() < 0.001);
    }

    #[test]
    fn normalize_font_size_clamped_to_min() {
        let result = normalize_editor_preferences(make_prefs(&default_editor_font_family(), 5, 1.6, 2, false));
        assert_eq!(result.font_size, 11);
    }

    #[test]
    fn normalize_font_size_clamped_to_max() {
        let result = normalize_editor_preferences(make_prefs(&default_editor_font_family(), 100, 1.6, 2, false));
        assert_eq!(result.font_size, 20);
    }

    #[test]
    fn normalize_tab_size_4_and_8_pass_through() {
        for size in [4u8, 8u8] {
            let result = normalize_editor_preferences(make_prefs(&default_editor_font_family(), 13, 1.6, size, false));
            assert_eq!(result.tab_size, size, "tab_size {size} should pass through");
        }
    }

    #[test]
    fn normalize_tab_size_invalid_defaults_to_2() {
        for size in [0u8, 1u8, 3u8, 5u8, 6u8, 7u8, 9u8] {
            let result = normalize_editor_preferences(make_prefs(&default_editor_font_family(), 13, 1.6, size, false));
            assert_eq!(result.tab_size, 2, "tab_size {size} should normalize to 2");
        }
    }

    #[test]
    fn normalize_word_wrap_preserved() {
        let result = normalize_editor_preferences(make_prefs(&default_editor_font_family(), 13, 1.6, 2, true));
        assert!(result.word_wrap);
    }

    #[test]
    fn app_config_default_has_expected_editor_values() {
        let cfg = AppConfig::default();
        assert_eq!(cfg.editor_font_size, 13);
        assert_eq!(cfg.editor_line_height, 1.6);
        assert_eq!(cfg.editor_tab_size, 2);
        assert!(!cfg.editor_word_wrap);
        assert_eq!(cfg.model_provider, Some("ollama".to_string()));
        assert!(cfg.github_token.is_none());
        assert!(cfg.local_server_token.is_none());
        assert!(cfg.last_project_path.is_none());
    }

    #[test]
    fn editor_preferences_from_default_config() {
        let cfg = AppConfig::default();
        let prefs = editor_preferences_from_config(&cfg);
        assert_eq!(prefs.font_size, 13);
        assert_eq!(prefs.tab_size, 2);
        assert!(!prefs.word_wrap);
        assert!(prefs.font_family.contains("JetBrains Mono"));
    }

    #[test]
    fn apply_editor_preferences_mutates_config() {
        let mut cfg = AppConfig::default();
        let prefs = make_prefs(
            "\"IBM Plex Mono\", \"JetBrains Mono\", Menlo, Monaco, monospace",
            15,
            1.8,
            4,
            true,
        );
        apply_editor_preferences(&mut cfg, prefs);
        assert_eq!(cfg.editor_font_size, 15);
        assert!((cfg.editor_line_height - 1.8_f32).abs() < 0.001);
        assert_eq!(cfg.editor_tab_size, 4);
        assert!(cfg.editor_word_wrap);
    }

    #[test]
    fn apply_editor_preferences_normalizes_before_storing() {
        let mut cfg = AppConfig::default();
        let prefs = make_prefs(&default_editor_font_family(), 13, 1.6, 3, false);
        apply_editor_preferences(&mut cfg, prefs);
        assert_eq!(cfg.editor_tab_size, 2);
    }

    #[test]
    fn default_project_path_from_prefers_home() {
        let result = default_project_path_from(
            Some(PathBuf::from("/tmp/home")),
            Some(PathBuf::from("/tmp/current")),
        );
        assert_eq!(result, "/tmp/home");
    }

    #[test]
    fn default_project_path_from_uses_current_dir_without_home() {
        let result = default_project_path_from(None, Some(PathBuf::from("/tmp/current")));
        assert_eq!(result, "/tmp/current");
    }

    #[test]
    fn default_project_path_from_falls_back_to_dot() {
        let result = default_project_path_from(None, None);
        assert_eq!(result, ".");
    }

    #[test]
    fn project_path_for_server_with_env_prefers_non_empty_env() {
        let mut cfg = AppConfig::default();
        cfg.last_project_path = Some("/workspace/from-config".to_string());
        let result = project_path_for_server_with_env(&cfg, Some("/workspace/from-env".to_string()));
        assert_eq!(result, "/workspace/from-env");
    }

    #[test]
    fn project_path_for_server_with_env_uses_config_when_env_blank() {
        let mut cfg = AppConfig::default();
        cfg.last_project_path = Some("/workspace/from-config".to_string());
        let result = project_path_for_server_with_env(&cfg, Some("   ".to_string()));
        assert_eq!(result, "/workspace/from-config");
    }

    #[test]
    fn project_path_for_server_with_env_falls_back_when_no_env_or_config() {
        let cfg = AppConfig::default();
        let result = project_path_for_server_with_env(&cfg, None);
        assert!(!result.is_empty());
    }
}
