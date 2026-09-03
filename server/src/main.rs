use axum::{
    extract::{Path, State},
    http::{HeaderMap, StatusCode},
    response::{Html, Json},
    routing::{delete, get, post},
    Router,
};
use rusqlite::{params, Connection};
use serde::{Deserialize, Serialize};
use std::net::SocketAddr;
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PingResult {
    pub name: String,
    pub target: String,
    pub latency_ms: Option<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PingTarget {
    pub id: i64,
    pub name: String,
    pub address: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CreatePingTargetReq {
    pub name: String,
    pub address: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AdminLoginReq {
    pub username: String,
    pub password: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReportPayload {
    pub token: String,
    pub id: String,
    pub hostname: String,
    pub os_name: String,
    pub arch: String,
    pub cpu_name: String,
    pub cpu_cores: usize,
    pub cpu_usage: f32,
    pub memory_used: u64,
    pub memory_total: u64,
    pub swap_used: u64,
    pub swap_total: u64,
    pub disk_used: u64,
    pub disk_total: u64,
    pub net_rx_bps: u64,
    pub net_tx_bps: u64,
    pub net_rx_total: u64,
    pub net_tx_total: u64,
    pub uptime_secs: u64,
    #[serde(default)]
    pub pings: Vec<PingResult>,
}

#[derive(Debug, Clone, Serialize)]
pub struct NodeInfo {
    pub id: String,
    pub hostname: String,
    pub os_name: String,
    pub arch: String,
    pub cpu_name: String,
    pub cpu_cores: usize,
    pub cpu_usage: f32,
    pub memory_used: u64,
    pub memory_total: u64,
    pub swap_used: u64,
    pub swap_total: u64,
    pub disk_used: u64,
    pub disk_total: u64,
    pub net_rx_bps: u64,
    pub net_tx_bps: u64,
    pub net_rx_total: u64,
    pub net_tx_total: u64,
    pub uptime_secs: u64,
    pub is_online: bool,
    pub last_seen_secs_ago: u64,
    pub pings: Vec<PingResult>,
}

pub struct Db {
    conn: Mutex<Connection>,
}

impl Db {
    pub fn open(path: &str) -> rusqlite::Result<Self> {
        let conn = Connection::open(path)?;
        conn.execute_batch(
            "
            PRAGMA journal_mode = WAL;
            PRAGMA synchronous = NORMAL;

            CREATE TABLE IF NOT EXISTS nodes (
                id TEXT PRIMARY KEY,
                hostname TEXT NOT NULL,
                os_name TEXT NOT NULL,
                arch TEXT NOT NULL,
                cpu_name TEXT NOT NULL,
                cpu_cores INTEGER NOT NULL,
                cpu_usage REAL NOT NULL,
                memory_used INTEGER NOT NULL,
                memory_total INTEGER NOT NULL,
                swap_used INTEGER NOT NULL,
                swap_total INTEGER NOT NULL,
                disk_used INTEGER NOT NULL,
                disk_total INTEGER NOT NULL,
                net_rx_bps INTEGER NOT NULL,
                net_tx_bps INTEGER NOT NULL,
                net_rx_total INTEGER NOT NULL,
                net_tx_total INTEGER NOT NULL,
                uptime_secs INTEGER NOT NULL,
                last_seen INTEGER NOT NULL,
                pings_json TEXT NOT NULL DEFAULT '[]'
            );

            CREATE TABLE IF NOT EXISTS metrics_history (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                node_id TEXT NOT NULL,
                cpu_usage REAL NOT NULL,
                memory_used INTEGER NOT NULL,
                memory_total INTEGER NOT NULL,
                net_rx_bps INTEGER NOT NULL,
                net_tx_bps INTEGER NOT NULL,
                recorded_at INTEGER NOT NULL
            );

            CREATE TABLE IF NOT EXISTS ping_targets (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                address TEXT NOT NULL
            );

            CREATE INDEX IF NOT EXISTS idx_metrics_node_time ON metrics_history(node_id, recorded_at);
            ",
        )?;

        // Seed default ping targets if empty
        let count: i64 = conn
            .query_row("SELECT COUNT(*) FROM ping_targets", [], |r| r.get(0))
            .unwrap_or(0);
        if count == 0 {
            conn.execute(
                "INSERT INTO ping_targets (name, address) VALUES 
                 ('Cloudflare DNS', '1.1.1.1:53'),
                 ('Google DNS', '8.8.8.8:53')",
                [],
            )?;
        }

        Ok(Self {
            conn: Mutex::new(conn),
        })
    }

    pub fn save_report(&self, p: &ReportPayload) -> rusqlite::Result<()> {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs() as i64;

        let pings_json = serde_json::to_string(&p.pings).unwrap_or_else(|_| "[]".to_string());
        let conn = self.conn.lock().unwrap();

        conn.execute(
            "INSERT OR REPLACE INTO nodes (
                id, hostname, os_name, arch, cpu_name, cpu_cores, cpu_usage,
                memory_used, memory_total, swap_used, swap_total,
                disk_used, disk_total, net_rx_bps, net_tx_bps, net_rx_total, net_tx_total,
                uptime_secs, last_seen, pings_json
            ) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16, ?17, ?18, ?19, ?20)",
            params![
                p.id,
                p.hostname,
                p.os_name,
                p.arch,
                p.cpu_name,
                p.cpu_cores as i64,
                p.cpu_usage as f64,
                p.memory_used as i64,
                p.memory_total as i64,
                p.swap_used as i64,
                p.swap_total as i64,
                p.disk_used as i64,
                p.disk_total as i64,
                p.net_rx_bps as i64,
                p.net_tx_bps as i64,
                p.net_rx_total as i64,
                p.net_tx_total as i64,
                p.uptime_secs as i64,
                now,
                pings_json,
            ],
        )?;

        conn.execute(
            "INSERT INTO metrics_history (node_id, cpu_usage, memory_used, memory_total, net_rx_bps, net_tx_bps, recorded_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)",
            params![
                p.id,
                p.cpu_usage as f64,
                p.memory_used as i64,
                p.memory_total as i64,
                p.net_rx_bps as i64,
                p.net_tx_bps as i64,
                now,
            ],
        )?;

        Ok(())
    }

    pub fn get_nodes(&self) -> rusqlite::Result<Vec<NodeInfo>> {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs() as i64;

        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare(
            "SELECT id, hostname, os_name, arch, cpu_name, cpu_cores, cpu_usage,
                    memory_used, memory_total, swap_used, swap_total,
                    disk_used, disk_total, net_rx_bps, net_tx_bps, net_rx_total, net_tx_total,
                    uptime_secs, last_seen, pings_json
             FROM nodes ORDER BY hostname ASC",
        )?;

        let node_iter = stmt.query_map([], |row| {
            let last_seen: i64 = row.get(18)?;
            let elapsed = (now - last_seen).max(0) as u64;
            let is_online = elapsed <= 8;
            let pings_json: String = row.get(19).unwrap_or_else(|_| "[]".to_string());
            let pings: Vec<PingResult> = serde_json::from_str(&pings_json).unwrap_or_default();

            Ok(NodeInfo {
                id: row.get(0)?,
                hostname: row.get(1)?,
                os_name: row.get(2)?,
                arch: row.get(3)?,
                cpu_name: row.get(4)?,
                cpu_cores: row.get::<_, i64>(5)? as usize,
                cpu_usage: row.get::<_, f64>(6)? as f32,
                memory_used: row.get::<_, i64>(7)? as u64,
                memory_total: row.get::<_, i64>(8)? as u64,
                swap_used: row.get::<_, i64>(9)? as u64,
                swap_total: row.get::<_, i64>(10)? as u64,
                disk_used: row.get::<_, i64>(11)? as u64,
                disk_total: row.get::<_, i64>(12)? as u64,
                net_rx_bps: row.get::<_, i64>(13)? as u64,
                net_tx_bps: row.get::<_, i64>(14)? as u64,
                net_rx_total: row.get::<_, i64>(15)? as u64,
                net_tx_total: row.get::<_, i64>(16)? as u64,
                uptime_secs: row.get::<_, i64>(17)? as u64,
                is_online,
                last_seen_secs_ago: elapsed,
                pings,
            })
        })?;

        let mut result = Vec::new();
        for node in node_iter {
            result.push(node?);
        }
        Ok(result)
    }

    pub fn delete_node(&self, id: &str) -> rusqlite::Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute("DELETE FROM nodes WHERE id = ?1", params![id])?;
        Ok(())
    }

    pub fn get_ping_targets(&self) -> rusqlite::Result<Vec<PingTarget>> {
        let conn = self.conn.lock().unwrap();
        let mut stmt = conn.prepare("SELECT id, name, address FROM ping_targets ORDER BY id ASC")?;
        let iter = stmt.query_map([], |row| {
            Ok(PingTarget {
                id: row.get(0)?,
                name: row.get(1)?,
                address: row.get(2)?,
            })
        })?;

        let mut result = Vec::new();
        for target in iter {
            result.push(target?);
        }
        Ok(result)
    }

    pub fn add_ping_target(&self, name: &str, address: &str) -> rusqlite::Result<i64> {
        let conn = self.conn.lock().unwrap();
        conn.execute(
            "INSERT INTO ping_targets (name, address) VALUES (?1, ?2)",
            params![name, address],
        )?;
        Ok(conn.last_insert_rowid())
    }

    pub fn delete_ping_target(&self, id: i64) -> rusqlite::Result<()> {
        let conn = self.conn.lock().unwrap();
        conn.execute("DELETE FROM ping_targets WHERE id = ?1", params![id])?;
        Ok(())
    }
}

struct AppState {
    auth_token: String,
    admin_user: String,
    admin_password: String,
    db: Arc<Db>,
}

#[tokio::main]
async fn main() {
    let port = std::env::var("VIBEMONITOR_PORT")
        .or_else(|_| std::env::var("PORT"))
        .unwrap_or_else(|_| "8080".to_string())
        .parse::<u16>()
        .expect("VIBEMONITOR_PORT 必须是有效端口号");

    let auth_token = std::env::var("VIBEMONITOR_TOKEN")
        .unwrap_or_else(|_| "vibemonitor_secret_token".to_string());

    let admin_user = std::env::var("VIBEMONITOR_ADMIN_USER")
        .unwrap_or_else(|_| "admin".to_string());

    let admin_password = std::env::var("VIBEMONITOR_ADMIN_PASSWORD")
        .unwrap_or_else(|_| "admin123".to_string());

    let db_path = std::env::var("VIBEMONITOR_DB")
        .unwrap_or_else(|_| "vibemonitor.db".to_string());

    let db = Arc::new(Db::open(&db_path).expect("初始化 SQLite 数据库失败"));

    let state = Arc::new(AppState {
        auth_token,
        admin_user: admin_user.clone(),
        admin_password,
        db,
    });

    let app = Router::new()
        .route("/", get(index_handler))
        .route("/api/report", post(report_handler))
        .route("/api/nodes", get(get_nodes_handler))
        .route("/api/ping-targets", get(get_ping_targets_handler))
        .route("/api/admin/login", post(admin_login_handler))
        .route("/api/admin/nodes/:id", delete(admin_delete_node_handler))
        .route("/api/admin/ping-targets", post(admin_add_ping_target_handler))
        .route("/api/admin/ping-targets/:id", delete(admin_delete_ping_target_handler))
        .with_state(state);

    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    println!("==================================================");
    println!("  ⚡ VibeMonitor Server 已启动 (单管理员模式)");
    println!("  🗄️ SQLite 数据库: {} (WAL 模式)", db_path);
    println!("  👤 管理员账号: {}", admin_user);
    println!("  🔐 管理员密码: (通过环境变量 VIBEMONITOR_ADMIN_PASSWORD 设置)");
    println!("  🌐 Web 仪表盘: http://0.0.0.0:{}", port);
    println!("  📡 数据上报接口: http://0.0.0.0:{}/api/report", port);
    println!("==================================================");

    let listener = tokio::net::TcpListener::bind(addr).await.expect("绑定端口失败");
    axum::serve(listener, app).await.expect("服务运行异常");
}

async fn index_handler() -> Html<&'static str> {
    Html(include_str!("static/index.html"))
}

async fn report_handler(
    State(state): State<Arc<AppState>>,
    Json(payload): Json<ReportPayload>,
) -> StatusCode {
    if payload.token != state.auth_token {
        eprintln!("[警告] 拒绝未授权探针上报: id={}", payload.id);
        return StatusCode::UNAUTHORIZED;
    }

    let db = state.db.clone();
    tokio::task::spawn_blocking(move || {
        if let Err(e) = db.save_report(&payload) {
            eprintln!("[数据库错误] 写入节点失败: {}", e);
        }
    })
    .await
    .ok();

    StatusCode::OK
}

async fn get_nodes_handler(State(state): State<Arc<AppState>>) -> Json<Vec<NodeInfo>> {
    let db = state.db.clone();
    let nodes = tokio::task::spawn_blocking(move || db.get_nodes().unwrap_or_default())
        .await
        .unwrap_or_default();

    Json(nodes)
}

async fn get_ping_targets_handler(State(state): State<Arc<AppState>>) -> Json<Vec<PingTarget>> {
    let db = state.db.clone();
    let targets = tokio::task::spawn_blocking(move || db.get_ping_targets().unwrap_or_default())
        .await
        .unwrap_or_default();

    Json(targets)
}

// ---------------- Single Admin Handlers ----------------

fn check_admin_auth(headers: &HeaderMap, state: &AppState) -> bool {
    let user_match = headers
        .get("X-Admin-User")
        .and_then(|v| v.to_str().ok())
        .map(|u| u == state.admin_user)
        .unwrap_or(false);

    let pwd_match = headers
        .get("X-Admin-Password")
        .and_then(|v| v.to_str().ok())
        .map(|p| p == state.admin_password)
        .unwrap_or(false);

    user_match && pwd_match
}

async fn admin_login_handler(
    State(state): State<Arc<AppState>>,
    Json(req): Json<AdminLoginReq>,
) -> StatusCode {
    if req.username == state.admin_user && req.password == state.admin_password {
        StatusCode::OK
    } else {
        StatusCode::UNAUTHORIZED
    }
}

async fn admin_delete_node_handler(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Path(id): Path<String>,
) -> StatusCode {
    if !check_admin_auth(&headers, &state) {
        return StatusCode::UNAUTHORIZED;
    }

    let db = state.db.clone();
    match tokio::task::spawn_blocking(move || db.delete_node(&id)).await {
        Ok(Ok(_)) => StatusCode::OK,
        _ => StatusCode::INTERNAL_SERVER_ERROR,
    }
}

async fn admin_add_ping_target_handler(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Json(req): Json<CreatePingTargetReq>,
) -> Result<Json<PingTarget>, StatusCode> {
    if !check_admin_auth(&headers, &state) {
        return Err(StatusCode::UNAUTHORIZED);
    }

    let name = req.name.clone();
    let address = req.address.clone();
    let db = state.db.clone();

    let res = tokio::task::spawn_blocking(move || {
        let id = db.add_ping_target(&name, &address)?;
        Ok::<PingTarget, rusqlite::Error>(PingTarget { id, name, address })
    })
    .await
    .map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?
    .map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;

    Ok(Json(res))
}

async fn admin_delete_ping_target_handler(
    State(state): State<Arc<AppState>>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> StatusCode {
    if !check_admin_auth(&headers, &state) {
        return StatusCode::UNAUTHORIZED;
    }

    let db = state.db.clone();
    match tokio::task::spawn_blocking(move || db.delete_ping_target(id)).await {
        Ok(Ok(_)) => StatusCode::OK,
        _ => StatusCode::INTERNAL_SERVER_ERROR,
    }
}
