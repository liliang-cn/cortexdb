//! Typed gRPC client for the CortexDB sidecar (`cortexdb-grpc`).
//!
//! ```no_run
//! # async fn demo() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
//! use cortexdb_client::CortexClient;
//! let client = CortexClient::builder("http://127.0.0.1:47821")
//!     .token("s3cret")
//!     .connect()
//!     .await?;
//! let info = client.admin().info(cortexdb_client::proto::InfoRequest {}).await?;
//! println!("server version: {}", info.into_inner().version);
//! # Ok(()) }
//! ```

mod client;
#[cfg(feature = "managed-server")]
pub mod sidecar;

pub use client::{AuthInterceptor, CortexClient, CortexClientBuilder};

/// Generated protobuf/tonic types for `cortexdb.v1`.
pub mod proto {
    include!("gen/cortexdb.v1.rs");
}
