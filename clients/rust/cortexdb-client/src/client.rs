use tonic::codegen::InterceptedService;
use tonic::metadata::{Ascii, MetadataValue};
use tonic::service::Interceptor;
use tonic::transport::{Channel, Endpoint};
use tonic::{Request, Status};

use crate::proto::admin_service_client::AdminServiceClient;
use crate::proto::graph_rag_service_client::GraphRagServiceClient;
use crate::proto::knowledge_graph_service_client::KnowledgeGraphServiceClient;
use crate::proto::knowledge_service_client::KnowledgeServiceClient;
use crate::proto::memory_service_client::MemoryServiceClient;
use crate::proto::tools_service_client::ToolsServiceClient;

/// Adds `authorization: Bearer <token>` to every request when a token is set.
#[derive(Clone)]
pub struct AuthInterceptor {
    token: Option<MetadataValue<Ascii>>,
}

impl Interceptor for AuthInterceptor {
    fn call(&mut self, mut request: Request<()>) -> Result<Request<()>, Status> {
        if let Some(token) = &self.token {
            request
                .metadata_mut()
                .insert("authorization", token.clone());
        }
        Ok(request)
    }
}

type Svc = InterceptedService<Channel, AuthInterceptor>;

/// One connection to a CortexDB sidecar, exposing all service clients.
#[derive(Clone)]
pub struct CortexClient {
    channel: Channel,
    interceptor: AuthInterceptor,
}

/// Builder for [`CortexClient`].
pub struct CortexClientBuilder {
    endpoint: String,
    token: Option<String>,
}

impl CortexClientBuilder {
    /// Authenticate every request with this bearer token.
    pub fn token(mut self, token: impl Into<String>) -> Self {
        self.token = Some(token.into());
        self
    }

    /// Connect to the sidecar.
    pub async fn connect(self) -> Result<CortexClient, Box<dyn std::error::Error + Send + Sync>> {
        let channel = Endpoint::from_shared(self.endpoint)?.connect().await?;
        let token = match self.token {
            Some(t) => Some(format!("Bearer {t}").parse()?),
            None => None,
        };
        Ok(CortexClient {
            channel,
            interceptor: AuthInterceptor { token },
        })
    }
}

impl CortexClient {
    /// Connect without authentication.
    pub async fn connect(
        endpoint: impl Into<String>,
    ) -> Result<Self, Box<dyn std::error::Error + Send + Sync>> {
        Self::builder(endpoint).connect().await
    }

    /// Start building a connection (set a token, then `connect`).
    pub fn builder(endpoint: impl Into<String>) -> CortexClientBuilder {
        CortexClientBuilder {
            endpoint: endpoint.into(),
            token: None,
        }
    }

    fn svc(&self) -> Svc {
        InterceptedService::new(self.channel.clone(), self.interceptor.clone())
    }

    /// AdminService: health + server info.
    pub fn admin(&self) -> AdminServiceClient<Svc> {
        AdminServiceClient::new(self.svc())
    }

    /// KnowledgeService: durable RAG knowledge save/search.
    pub fn knowledge(&self) -> KnowledgeServiceClient<Svc> {
        KnowledgeServiceClient::new(self.svc())
    }

    /// MemoryService: scoped agent memory.
    pub fn memory(&self) -> MemoryServiceClient<Svc> {
        MemoryServiceClient::new(self.svc())
    }

    /// KnowledgeGraphService: RDF triples, SPARQL, SHACL, inference, ontology.
    pub fn graph(&self) -> KnowledgeGraphServiceClient<Svc> {
        KnowledgeGraphServiceClient::new(self.svc())
    }

    /// GraphRagService: GraphRAG ingest/search + text search.
    pub fn graphrag(&self) -> GraphRagServiceClient<Svc> {
        GraphRagServiceClient::new(self.svc())
    }

    /// ToolsService: generic tool dispatch (same surface as MCP).
    pub fn tools(&self) -> ToolsServiceClient<Svc> {
        ToolsServiceClient::new(self.svc())
    }
}
