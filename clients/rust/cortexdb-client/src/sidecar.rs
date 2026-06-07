//! Optional sidecar management: resolve or download the `cortexdb-grpc` binary,
//! then spawn it with a fresh random token. Enabled by the `managed-server` feature.

use std::path::{Path, PathBuf};

use crate::CortexClient;

/// CortexDB release tag (without the leading `v`) whose sidecar binaries this
/// crate downloads. The repo is versioned independently of this crate — bump
/// this constant when releasing against a newer sidecar.
pub const SIDECAR_VERSION: &str = "2.21.0";

const RELEASE_BASE: &str = "https://github.com/liliang-cn/cortexdb/releases/download";

/// Errors from sidecar resolution, download, or spawn.
#[derive(Debug)]
pub enum SidecarError {
    /// Filesystem or process error.
    Io(std::io::Error),
    /// Download failed (network or HTTP status).
    Download(String),
    /// SHA-256 checksum mismatch for the named asset.
    Checksum(String),
    /// No prebuilt binary exists for this OS/arch.
    UnsupportedPlatform(String),
    /// The spawned process never answered Health.
    NotHealthy,
    /// Any other error (e.g. connection failures).
    Other(Box<dyn std::error::Error + Send + Sync>),
}

impl std::fmt::Display for SidecarError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Io(e) => write!(f, "io: {e}"),
            Self::Download(msg) => write!(f, "download failed: {msg}"),
            Self::Checksum(asset) => write!(f, "checksum mismatch for {asset}"),
            Self::UnsupportedPlatform(p) => write!(f, "unsupported platform: {p}"),
            Self::NotHealthy => write!(f, "sidecar did not become healthy"),
            Self::Other(e) => write!(f, "{e}"),
        }
    }
}

impl std::error::Error for SidecarError {}

impl From<std::io::Error> for SidecarError {
    fn from(e: std::io::Error) -> Self {
        Self::Io(e)
    }
}

/// A resolved sidecar binary.
pub struct Sidecar {
    binary: PathBuf,
}

/// A running, authenticated sidecar process. Kills the child on drop.
pub struct RunningSidecar {
    child: tokio::process::Child,
    endpoint: String,
    token: String,
}

fn platform_asset() -> Result<String, SidecarError> {
    let os = match std::env::consts::OS {
        "macos" => "darwin",
        "linux" => "linux",
        "windows" => "windows",
        other => return Err(SidecarError::UnsupportedPlatform(other.into())),
    };
    let arch = match std::env::consts::ARCH {
        "x86_64" => "amd64",
        "aarch64" => "arm64",
        other => return Err(SidecarError::UnsupportedPlatform(other.into())),
    };
    Ok(format!("cortexdb-grpc_{os}_{arch}.tar.gz"))
}

impl Sidecar {
    /// Resolve a binary: `$CORTEXDB_GRPC_BIN` → `$PATH` → cached download.
    pub async fn ensure() -> Result<Self, SidecarError> {
        Self::ensure_with_base_url(RELEASE_BASE).await
    }

    /// Like [`Sidecar::ensure`], with an overridable release base URL (used by tests).
    pub async fn ensure_with_base_url(base: &str) -> Result<Self, SidecarError> {
        if let Ok(p) = std::env::var("CORTEXDB_GRPC_BIN") {
            return Ok(Self { binary: p.into() });
        }
        if let Ok(p) = which("cortexdb-grpc") {
            return Ok(Self { binary: p });
        }
        let cache = dirs::cache_dir()
            .unwrap_or_else(std::env::temp_dir)
            .join("cortexdb")
            .join("bin")
            .join(format!("v{SIDECAR_VERSION}"));
        let exe = cache.join(if cfg!(windows) {
            "cortexdb-grpc.exe"
        } else {
            "cortexdb-grpc"
        });
        if exe.exists() {
            return Ok(Self { binary: exe });
        }
        download_release(base, &cache, &exe).await?;
        Ok(Self { binary: exe })
    }

    /// Use an explicit binary path, skipping resolution.
    pub fn from_binary(path: impl Into<PathBuf>) -> Self {
        Self {
            binary: path.into(),
        }
    }

    /// The resolved binary path.
    pub fn binary(&self) -> &Path {
        &self.binary
    }

    /// Spawn the sidecar on an ephemeral port with a fresh random token and
    /// wait until Health responds. Managed mode is authenticated by default.
    pub async fn spawn(&self, db_path: impl AsRef<Path>) -> Result<RunningSidecar, SidecarError> {
        let port = pick_free_port()?;
        let addr = format!("127.0.0.1:{port}");
        let token: String = {
            use rand::Rng;
            let mut rng = rand::rng();
            (0..32)
                .map(|_| rng.sample(rand::distr::Alphanumeric) as char)
                .collect()
        };
        let child = tokio::process::Command::new(&self.binary)
            .arg("-db")
            .arg(db_path.as_ref())
            .arg("-addr")
            .arg(&addr)
            .arg("-token")
            .arg(&token)
            .kill_on_drop(true)
            .spawn()?;
        let endpoint = format!("http://{addr}");
        let running = RunningSidecar {
            child,
            endpoint,
            token,
        };
        running.wait_healthy().await?;
        Ok(running)
    }
}

impl RunningSidecar {
    /// gRPC endpoint of the running sidecar (`http://127.0.0.1:<port>`).
    pub fn endpoint(&self) -> &str {
        &self.endpoint
    }

    /// The auto-generated bearer token.
    pub fn token(&self) -> &str {
        &self.token
    }

    /// Connect a pre-authenticated client to this sidecar.
    pub async fn client(&self) -> Result<CortexClient, SidecarError> {
        CortexClient::builder(self.endpoint.clone())
            .token(self.token.clone())
            .connect()
            .await
            .map_err(SidecarError::Other)
    }

    /// Kill the child process explicitly (it is also killed on drop).
    pub async fn shutdown(mut self) -> Result<(), SidecarError> {
        self.child.kill().await?;
        Ok(())
    }

    async fn wait_healthy(&self) -> Result<(), SidecarError> {
        for _ in 0..50 {
            if let Ok(c) = self.client().await {
                if c.admin()
                    .health(crate::proto::HealthRequest {})
                    .await
                    .is_ok()
                {
                    return Ok(());
                }
            }
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        }
        Err(SidecarError::NotHealthy)
    }
}

fn pick_free_port() -> std::io::Result<u16> {
    let l = std::net::TcpListener::bind("127.0.0.1:0")?;
    Ok(l.local_addr()?.port())
}

fn which(name: &str) -> Result<PathBuf, std::io::Error> {
    let paths = std::env::var_os("PATH").unwrap_or_default();
    for dir in std::env::split_paths(&paths) {
        let candidate = dir.join(name);
        if candidate.is_file() {
            return Ok(candidate);
        }
    }
    Err(std::io::Error::new(
        std::io::ErrorKind::NotFound,
        name.to_string(),
    ))
}

async fn download_release(base: &str, cache: &Path, exe: &Path) -> Result<(), SidecarError> {
    let asset = platform_asset()?;
    let url = format!("{base}/v{SIDECAR_VERSION}/{asset}");
    let sum_url = format!("{url}.sha256");

    let bytes = fetch(&url).await?;
    let expected = String::from_utf8_lossy(&fetch(&sum_url).await?)
        .split_whitespace()
        .next()
        .unwrap_or_default()
        .to_string();
    let actual = {
        use sha2::{Digest, Sha256};
        hex::encode(Sha256::digest(&bytes))
    };
    if actual != expected {
        return Err(SidecarError::Checksum(asset));
    }

    std::fs::create_dir_all(cache)?;
    let gz = flate2::read::GzDecoder::new(std::io::Cursor::new(bytes));
    tar::Archive::new(gz).unpack(cache)?;
    if !exe.exists() {
        return Err(SidecarError::Download(format!(
            "archive did not contain {}",
            exe.display()
        )));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(exe, std::fs::Permissions::from_mode(0o755))?;
    }
    Ok(())
}

async fn fetch(url: &str) -> Result<Vec<u8>, SidecarError> {
    let resp = reqwest::get(url)
        .await
        .map_err(|e| SidecarError::Download(e.to_string()))?;
    if !resp.status().is_success() {
        return Err(SidecarError::Download(format!(
            "{url}: HTTP {}",
            resp.status()
        )));
    }
    Ok(resp
        .bytes()
        .await
        .map_err(|e| SidecarError::Download(e.to_string()))?
        .to_vec())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn platform_asset_is_known() {
        let asset = platform_asset().expect("supported platform");
        assert!(asset.starts_with("cortexdb-grpc_"));
        assert!(asset.ends_with(".tar.gz"));
    }

    #[test]
    fn pick_free_port_works() {
        let port = pick_free_port().expect("free port");
        assert!(port > 0);
    }

    #[test]
    fn which_finds_cargo() {
        assert!(which("cargo").is_ok());
    }
}
