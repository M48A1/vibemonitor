use serde::{Deserialize, Serialize};
use std::env;
use std::net::{TcpStream, ToSocketAddrs};
use std::thread::sleep;
use std::time::{Duration, Instant};
use sysinfo::{Disks, Networks, System};

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
    pub pings: Vec<PingResult>,
}

fn tcp_ping(addr_str: &str, timeout: Duration) -> Option<u64> {
    if let Ok(mut addrs) = addr_str.to_socket_addrs() {
        if let Some(addr) = addrs.next() {
            let start = Instant::now();
            if TcpStream::connect_timeout(&addr, timeout).is_ok() {
                return Some(start.elapsed().as_millis() as u64);
            }
        }
    }
    None
}

fn fetch_ping_targets(base_url: &str) -> Vec<PingTarget> {
    let url = format!("{}/api/ping-targets", base_url.trim_end_matches('/'));
    match ureq::get(&url).timeout(Duration::from_secs(3)).call() {
        Ok(res) => res.into_json::<Vec<PingTarget>>().unwrap_or_default(),
        Err(_) => Vec::new(),
    }
}

fn main() {
    let raw_server = env::var("VIBEMONITOR_SERVER")
        .or_else(|_| env::var("KOMARI_SERVER"))
        .unwrap_or_else(|_| "http://127.0.0.1:8080".to_string());

    let base_server_url = raw_server.trim_end_matches('/').to_string();
    let server_report_url = if base_server_url.ends_with("/api/report") {
        base_server_url.clone()
    } else {
        format!("{}/api/report", base_server_url)
    };

    let token = env::var("VIBEMONITOR_TOKEN")
        .or_else(|_| env::var("KOMARI_TOKEN"))
        .unwrap_or_else(|_| "vibemonitor_secret_token".to_string());

    let interval_secs = env::var("VIBEMONITOR_INTERVAL")
        .or_else(|_| env::var("KOMARI_INTERVAL"))
        .ok()
        .and_then(|s| s.parse::<u64>().ok())
        .unwrap_or(2);

    println!("==================================================");
    println!("  ⚡ VibeMonitor Agent 探针已启动");
    println!("  📡 服务端目标: {}", server_report_url);
    println!("  📶 TCP Ping 监控: 已启用");
    println!("  ⏱️ 采样周期: {} 秒", interval_secs);
    println!("==================================================");

    let mut sys = System::new_all();
    let mut networks = Networks::new_with_refreshed_list();
    let mut disks = Disks::new_with_refreshed_list();

    sys.refresh_all();
    let hostname = System::host_name().unwrap_or_else(|| "Unknown-Host".to_string());
    let node_id = env::var("VIBEMONITOR_NODE_ID")
        .or_else(|_| env::var("KOMARI_NODE_ID"))
        .unwrap_or_else(|_| hostname.clone());

    let os_name = format!(
        "{} {}",
        System::name().unwrap_or_else(|| "Linux".to_string()),
        System::os_version().unwrap_or_default()
    )
    .trim()
    .to_string();

    let arch = System::cpu_arch().unwrap_or_else(|| env::consts::ARCH.to_string());
    let cpu_name = sys
        .cpus()
        .first()
        .map(|c| c.brand().trim().to_string())
        .unwrap_or_default();
    let cpu_cores = sys.cpus().len();

    let mut last_net_rx = 0u64;
    let mut last_net_tx = 0u64;
    let mut last_time = Instant::now();

    networks.refresh();
    for (_, data) in networks.iter() {
        last_net_rx += data.total_received();
        last_net_tx += data.total_transmitted();
    }

    let mut ping_targets = fetch_ping_targets(&base_server_url);
    let mut cycle_counter = 0u64;

    loop {
        sleep(Duration::from_secs(interval_secs));
        let now = Instant::now();
        let elapsed_secs = now.duration_since(last_time).as_secs_f64().max(0.1);
        last_time = now;
        cycle_counter += 1;

        // 每 15 个周期 (约 30 秒) 重新同步一次服务端的全局 Ping 目标配置
        if cycle_counter % 15 == 0 || ping_targets.is_empty() {
            let updated = fetch_ping_targets(&base_server_url);
            if !updated.is_empty() {
                ping_targets = updated;
            }
        }

        sys.refresh_cpu_usage();
        sys.refresh_memory();
        networks.refresh();
        disks.refresh();

        let cpu_usage = sys.global_cpu_info().cpu_usage();
        let memory_used = sys.used_memory();
        let memory_total = sys.total_memory();
        let swap_used = sys.used_swap();
        let swap_total = sys.total_swap();

        let mut disk_total = 0u64;
        let mut disk_available = 0u64;
        for disk in disks.iter() {
            disk_total += disk.total_space();
            disk_available += disk.available_space();
        }
        let disk_used = disk_total.saturating_sub(disk_available);

        let mut current_net_rx = 0u64;
        let mut current_net_tx = 0u64;
        for (_, data) in networks.iter() {
            current_net_rx += data.total_received();
            current_net_tx += data.total_transmitted();
        }

        let net_rx_bps = ((current_net_rx.saturating_sub(last_net_rx)) as f64 / elapsed_secs) as u64;
        let net_tx_bps = ((current_net_tx.saturating_sub(last_net_tx)) as f64 / elapsed_secs) as u64;
        last_net_rx = current_net_rx;
        last_net_tx = current_net_tx;

        // 执行 TCP Ping 测速
        let mut pings = Vec::new();
        for target in &ping_targets {
            let latency = tcp_ping(&target.address, Duration::from_millis(800));
            pings.push(PingResult {
                name: target.name.clone(),
                target: target.address.clone(),
                latency_ms: latency,
            });
        }

        let payload = ReportPayload {
            token: token.clone(),
            id: node_id.clone(),
            hostname: hostname.clone(),
            os_name: os_name.clone(),
            arch: arch.clone(),
            cpu_name: cpu_name.clone(),
            cpu_cores,
            cpu_usage,
            memory_used,
            memory_total,
            swap_used,
            swap_total,
            disk_used,
            disk_total,
            net_rx_bps,
            net_tx_bps,
            net_rx_total: current_net_rx,
            net_tx_total: current_net_tx,
            uptime_secs: System::uptime(),
            pings,
        };

        let _ = ureq::post(&server_report_url)
            .timeout(Duration::from_secs(4))
            .send_json(&payload);
    }
}
