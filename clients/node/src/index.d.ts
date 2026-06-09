import type { ChannelCredentials, Metadata } from '@grpc/grpc-js';

/** A unary RPC: pass a plain request object, await a plain response object. */
type Rpc = (request?: Record<string, any>) => Promise<any>;

/** A service sub-client: every RPC available in PascalCase and camelCase. */
type Service = Record<string, Rpc>;

export interface ConnectOptions {
  /** Bearer token applied to every request. */
  token?: string;
  /** TLS credentials; defaults to insecure (matches the sidecar's default). */
  credentials?: ChannelCredentials;
}

export class CortexClient {
  static connect(endpoint: string, opts?: ConnectOptions): CortexClient;
  constructor(endpoint: string, opts?: ConnectOptions);

  /** AdminService: Health, Info. */
  admin: Service;
  /** KnowledgeService: SaveKnowledge, SearchKnowledge, … */
  knowledge: Service;
  /** MemoryService: SaveMemory, SearchMemory, … */
  memory: Service;
  /** KnowledgeGraphService: SPARQL/RDF/SHACL/inference/ontology. */
  graph: Service;
  /** GraphRagService: GraphRAG ingest/search + text search. */
  graphrag: Service;
  /** ToolsService: generic tool dispatch (same surface as MCP). */
  tools: Service;

  close(): void;
}

export { Metadata };
