//! Regenerates ../cortexdb-client/src/gen from the vendored protos.
//! Run from clients/rust/: cargo run -p gen
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let out = std::path::Path::new("cortexdb-client/src/gen");
    std::fs::create_dir_all(out)?;
    tonic_prost_build::configure()
        .build_server(false)
        .out_dir(out)
        .compile_protos(
            &[
                "cortexdb-client/proto/cortexdb/v1/common.proto",
                "cortexdb-client/proto/cortexdb/v1/knowledge.proto",
                "cortexdb-client/proto/cortexdb/v1/memory.proto",
                "cortexdb-client/proto/cortexdb/v1/graph.proto",
                "cortexdb-client/proto/cortexdb/v1/graphrag.proto",
                "cortexdb-client/proto/cortexdb/v1/tools.proto",
                "cortexdb-client/proto/cortexdb/v1/admin.proto",
            ],
            &["cortexdb-client/proto"],
        )?;
    Ok(())
}
