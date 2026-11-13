use std::collections::VecDeque;
use std::fs::{self, File};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Manager, RunEvent, State, WindowEvent};
use url::Url;

#[cfg(target_os = "windows")]
use std::os::windows::process::CommandExt;

const DEFAULT_ADDR: &str = "127.0.0.1:18060";
const FALLBACK_ADDR: &str = "127.0.0.1:0";
const SHUTDOWN_WAIT: Duration = Duration::from_secs(15);

#[cfg(target_os = "windows")]
const CREATE_NO_WINDOW: u32 = 0x0800_0000;

struct SidecarState {
    child: Mutex<Option<Child>>,
    quitting: AtomicBool,
}

impl SidecarState {
    fn new() -> Self {
        Self {
            child: Mutex::new(None),
            quitting: AtomicBool::new(false),
        }
    }

    fn set_child(&self, child: Child) {
        let mut guard = self.child.lock().expect("sidecar mutex poisoned");
        *guard = Some(child);
    }

    fn stop(&self) {
        let mut guard = self.child.lock().expect("sidecar mutex poisoned");
        let Some(mut child) = guard.take() else {
            return;
        };
        drop(guard);

        // 关闭标准输入以触发 Go 端 -watch-stdin 优雅退出，等待浏览器与数据库落盘。
        drop(child.stdin.take());
        let deadline = Instant::now() + SHUTDOWN_WAIT;
        loop {
            match child.try_wait() {
                Ok(Some(_)) => return,
                Ok(None) if Instant::now() < deadline => {
                    std::thread::sleep(Duration::from_millis(100));
                }
                _ => {
                    let _ = child.kill();
                    let _ = child.wait();
                    return;
                }
            }
        }
    }
}

#[tauri::command]
fn quit_app(app: AppHandle, state: State<'_, SidecarState>) {
    shutdown_and_exit(&app, &state);
}

fn shutdown_and_exit(app: &AppHandle, state: &SidecarState) {
    state.quitting.store(true, Ordering::SeqCst);
    state.stop();
    app.exit(0);
}

fn show_main_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.unminimize();
        let _ = window.show();
        let _ = window.set_focus();
    }
}

fn sidecar_binary_name() -> &'static str {
    if cfg!(target_os = "windows") {
        "xiaohongshu-studio.exe"
    } else {
        "xiaohongshu-studio"
    }
}

fn resolve_sidecar_path() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|e| format!("无法定位桌面端可执行文件: {e}"))?;
    let dir = exe
        .parent()
        .ok_or_else(|| "无法确定桌面端程序所在目录".to_string())?;
    let path = dir.join(sidecar_binary_name());
    if path.is_file() {
        return Ok(path);
    }
    Err(format!("未找到内置后端程序: {}", path.display()))
}

fn expected_browser_binary_name() -> &'static str {
    if cfg!(target_os = "windows") {
        "chrome.exe"
    } else if cfg!(target_os = "macos") {
        "Chromium"
    } else {
        "chrome"
    }
}

fn contains_file_named(dir: &Path, target: &str) -> bool {
    let Ok(entries) = fs::read_dir(dir) else {
        return false;
    };
    for entry in entries.flatten() {
        let Ok(ft) = entry.file_type() else {
            continue;
        };
        let path = entry.path();
        if ft.is_dir() {
            if contains_file_named(&path, target) {
                return true;
            }
        } else if path.file_name().and_then(|n| n.to_str()) == Some(target) {
            return true;
        }
    }
    false
}

fn archive_stamp(resource_browser_dir: &Path, archive: &Path) -> Result<String, String> {
    let sha_path = resource_browser_dir.join("chromium.tar.gz.sha256");
    if let Ok(raw) = fs::read_to_string(&sha_path) {
        let trimmed = raw.trim();
        if !trimmed.is_empty() {
            return Ok(trimmed.to_string());
        }
    }
    let meta = fs::metadata(archive)
        .map_err(|e| format!("无法读取内置浏览器归档信息 {}: {e}", archive.display()))?;
    Ok(format!("len:{}", meta.len()))
}

fn extract_bundled_archive(
    resource_browser_dir: &Path,
    archive: &Path,
    data_dir: &Path,
    binary: &str,
) -> Result<PathBuf, String> {
    let browser_root = data_dir.join("browser");
    let current_dir = browser_root.join("current");
    let stamp_path = browser_root.join(".stamp");
    let expected_stamp = archive_stamp(resource_browser_dir, archive)?;

    if current_dir.is_dir()
        && fs::read_to_string(&stamp_path)
            .map(|s| s.trim() == expected_stamp)
            .unwrap_or(false)
        && contains_file_named(&current_dir, binary)
    {
        return Ok(current_dir);
    }

    fs::create_dir_all(&browser_root).map_err(|e| format!("创建内置浏览器运行目录失败: {e}"))?;
    let stage_dir = browser_root.join(format!("staging-{}", std::process::id()));
    let _ = fs::remove_dir_all(&stage_dir);
    fs::create_dir_all(&stage_dir).map_err(|e| format!("创建内置浏览器解压临时目录失败: {e}"))?;

    let status = Command::new("tar")
        .arg("-xzf")
        .arg(archive)
        .arg("-C")
        .arg(&stage_dir)
        .status()
        .map_err(|e| format!("调用 tar 解压内置浏览器失败: {e}"))?;
    if !status.success() {
        let _ = fs::remove_dir_all(&stage_dir);
        return Err(format!("解压内置浏览器失败，退出码: {status}"));
    }

    if !contains_file_named(&stage_dir, binary) {
        let _ = fs::remove_dir_all(&stage_dir);
        return Err(format!("解压后的内置浏览器中未找到 {binary}"));
    }

    let _ = fs::remove_file(&stamp_path);
    if current_dir.exists() {
        fs::remove_dir_all(&current_dir).map_err(|e| format!("清理旧版内置浏览器失败: {e}"))?;
    }
    fs::rename(&stage_dir, &current_dir).map_err(|e| format!("写入内置浏览器目录失败: {e}"))?;
    fs::write(&stamp_path, format!("{expected_stamp}\n"))
        .map_err(|e| format!("写入内置浏览器校验标记失败: {e}"))?;
    Ok(current_dir)
}

fn resolve_browser_dir(app: &AppHandle, data_dir: &Path) -> Result<PathBuf, String> {
    let resource_dir = app
        .path()
        .resource_dir()
        .map_err(|e| format!("无法获取应用资源目录: {e}"))?;
    let binary = expected_browser_binary_name();
    let candidates = [
        resource_dir.join("browser"),
        resource_dir.join("resources").join("browser"),
    ];
    for candidate in &candidates {
        if !candidate.is_dir() {
            continue;
        }
        if contains_file_named(candidate, binary) {
            return Ok(candidate.clone());
        }
        let archive = candidate.join("chromium.tar.gz");
        if archive.is_file() {
            return extract_bundled_archive(candidate, &archive, data_dir, binary);
        }
    }
    Err(format!(
        "安装包预置指纹浏览器缺失（未在 {} 中找到 {}）",
        resource_dir.display(),
        binary
    ))
}

struct BackendPaths {
    data_dir: PathBuf,
    accounts_dir: PathBuf,
    db_path: PathBuf,
    images_dir: PathBuf,
    cookies_path: PathBuf,
    log_path: PathBuf,
    browser_dir: PathBuf,
}

fn prepare_backend_paths(app: &AppHandle) -> Result<BackendPaths, String> {
    let data_dir = app
        .path()
        .app_local_data_dir()
        .map_err(|e| format!("无法确定本地数据目录: {e}"))?;
    fs::create_dir_all(&data_dir).map_err(|e| format!("创建本地数据目录失败: {e}"))?;
    let browser_dir = resolve_browser_dir(app, &data_dir)?;

    Ok(BackendPaths {
        accounts_dir: data_dir.join("accounts"),
        db_path: data_dir.join("studio.db"),
        images_dir: data_dir.join("uploads"),
        cookies_path: data_dir.join("cookies.json"),
        log_path: data_dir.join("studio.log"),
        data_dir,
        browser_dir,
    })
}

fn escape_js_string(value: &str) -> String {
    let mut out = String::with_capacity(value.len() + 2);
    out.push('"');
    for ch in value.chars() {
        match ch {
            '\\' => out.push_str("\\\\"),
            '"' => out.push_str("\\\""),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

fn report_startup_error(app: &AppHandle, message: &str) {
    if let Some(window) = app.get_webview_window("main") {
        let script = format!(
            "window.__xhsDesktopSetError && window.__xhsDesktopSetError({});",
            escape_js_string(message)
        );
        let _ = window.eval(&script);
    }
}

fn spawn_and_wait_addr(
    sidecar: &Path,
    paths: &BackendPaths,
    listen_addr: &str,
) -> Result<(Child, String), String> {
    let mut cmd = Command::new(sidecar);
    cmd.args(["-port", listen_addr, "-watch-stdin", "-print-addr"])
        .current_dir(&paths.data_dir)
        .env("XHS_ACCOUNTS_DIR", &paths.accounts_dir)
        .env("XHS_DB_PATH", &paths.db_path)
        .env("XHS_IMAGES_DIR", &paths.images_dir)
        .env("COOKIES_PATH", &paths.cookies_path)
        .env("XHS_BROWSER_DIR", &paths.browser_dir)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());

    #[cfg(target_os = "windows")]
    cmd.creation_flags(CREATE_NO_WINDOW);

    let mut child = cmd
        .spawn()
        .map_err(|e| format!("无法启动本地创作服务 ({}): {e}", sidecar.display()))?;

    let stdout = child
        .stdout
        .take()
        .ok_or_else(|| "无法读取本地服务输出".to_string())?;
    let stderr = child
        .stderr
        .take()
        .ok_or_else(|| "无法读取本地服务错误流".to_string())?;

    let tail = Arc::new(Mutex::new(VecDeque::<String>::with_capacity(20)));
    let tail_clone = Arc::clone(&tail);
    let log_path = paths.log_path.clone();
    std::thread::spawn(move || {
        let mut file = File::create(&log_path).ok();
        let reader = BufReader::new(stderr);
        for line in reader.lines().map_while(Result::ok) {
            if let Some(ref mut f) = file {
                let _ = writeln!(f, "{line}");
            }
            if let Ok(mut lines) = tail_clone.lock() {
                if lines.len() >= 15 {
                    lines.pop_front();
                }
                lines.push_back(line);
            }
        }
    });

    let mut reader = BufReader::new(stdout);
    let mut addr = None;
    loop {
        let mut line = String::new();
        match reader.read_line(&mut line) {
            Ok(0) | Err(_) => break,
            Ok(_) => {
                if let Some(value) = line.strip_prefix("XHS_STUDIO_ADDR=") {
                    let trimmed = value.trim();
                    if !trimmed.is_empty() {
                        addr = Some(trimmed.to_string());
                        break;
                    }
                }
            }
        }
    }
    if let Some(addr) = addr {
        // Gin 访问日志写 stdout；读端关闭会让 sidecar 收到 SIGPIPE 直接退出。
        std::thread::spawn(move || {
            let mut line = String::new();
            while matches!(reader.read_line(&mut line), Ok(n) if n > 0) {
                line.clear();
            }
        });
        return Ok((child, addr));
    }

    let status = child.wait().ok();
    let recent = tail
        .lock()
        .map(|lines| lines.iter().cloned().collect::<Vec<_>>().join("\n"))
        .unwrap_or_default();
    let detail = if recent.is_empty() {
        format!("退出状态: {status:?}")
    } else {
        recent
    };
    Err(format!(
        "本地服务未能就绪（日志位于 {}）：\n{detail}",
        paths.log_path.display()
    ))
}

fn start_backend(app: AppHandle) {
    let result: Result<String, String> = (|| {
        let sidecar = resolve_sidecar_path()?;
        let paths = prepare_backend_paths(&app)?;
        let (child, addr) = match spawn_and_wait_addr(&sidecar, &paths, DEFAULT_ADDR) {
            Ok(ok) => ok,
            Err(_) => spawn_and_wait_addr(&sidecar, &paths, FALLBACK_ADDR)?,
        };
        let state = app.state::<SidecarState>();
        if state.quitting.load(Ordering::SeqCst) {
            let mut c = child;
            drop(c.stdin.take());
            let _ = c.wait();
            return Err("应用正在退出".to_string());
        }
        state.set_child(child);
        Ok(addr)
    })();

    match result {
        Ok(addr) => {
            if let Some(window) = app.get_webview_window("main") {
                match Url::parse(&format!("http://{addr}/")) {
                    Ok(url) => {
                        if let Err(err) = window.navigate(url) {
                            report_startup_error(&app, &format!("打开工作台页面失败: {err}"));
                        }
                    }
                    Err(err) => {
                        report_startup_error(&app, &format!("本地服务地址无效 ({addr}): {err}"));
                    }
                }
            }
        }
        Err(message) => {
            report_startup_error(&app, &message);
        }
    }
}

fn setup_tray(app: &AppHandle) -> tauri::Result<()> {
    let show_item = MenuItem::with_id(app, "show", "打开红薯创作台", true, None::<&str>)?;
    let sep = PredefinedMenuItem::separator(app)?;
    let quit_item = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&show_item, &sep, &quit_item])?;

    let mut builder = TrayIconBuilder::with_id("main-tray")
        .menu(&menu)
        .tooltip("红薯创作台")
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match event.id.as_ref() {
            "show" => show_main_window(app),
            "quit" => {
                let state = app.state::<SidecarState>();
                shutdown_and_exit(app, &state);
            }
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                show_main_window(tray.app_handle());
            }
        });

    if let Some(icon) = app.default_window_icon() {
        builder = builder.icon(icon.clone());
    }
    builder.build(app)?;
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let app = tauri::Builder::default()
        .manage(SidecarState::new())
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            show_main_window(app);
        }))
        .invoke_handler(tauri::generate_handler![quit_app])
        .setup(|app| {
            setup_tray(app.handle())?;
            let handle = app.handle().clone();
            std::thread::spawn(move || start_backend(handle));
            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                let state = window.app_handle().state::<SidecarState>();
                if !state.quitting.load(Ordering::SeqCst) {
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building Xiaohongshu Studio desktop");

    app.run(|app_handle, event| {
        if let RunEvent::Exit = event {
            let state = app_handle.state::<SidecarState>();
            state.quitting.store(true, Ordering::SeqCst);
            state.stop();
        }
    });
}
