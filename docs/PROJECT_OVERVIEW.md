# CortexDB 项目总览

## 项目定位

CortexDB 是一个纯 Go 实现的单文件 AI 记忆与知识图谱库，由 Liang Li 创建。
CortexDB 使用 SQLite 作为存储内核（通过 modernc.org/sqlite，纯 Go、无 CGO）。
一个数据库文件同时容纳向量、FTS5 全文索引、RAG 知识、作用域代理记忆、
RDF 知识图谱与 MCP 工具定义。模块路径是 github.com/liliang-cn/cortexdb/v2。
CortexDB 不依赖任何 LLM SDK，所有 LLM 相关能力都通过接口注入。

## 架构分层

CortexDB 采用分层架构。pkg/core 是引擎层，提供 SQLiteStore、嵌入向量、
FTS5 与 HNSW、IVF、Flat 三种向量索引。pkg/graph 也是引擎层，提供属性图、
RDF 三元组、SPARQL 查询、RDFS 推理与 SHACL 校验。pkg/cortexdb 是公共门面，
封装 core 与 graph，应用代码默认依赖这一层。pkg/memoryflow 是工作流层，
负责对话转写摄取、记忆唤醒与日记。pkg/graphflow 是工作流层，负责语料到
知识图谱的抽取、构建、分析、报告与导出。pkg/importflow 是工作流层，
负责 CSV 与 MySQL、PostgreSQL 转储的结构化导入。pkg/agentmem 提供
不需要嵌入模型的 SQL 代理记忆，使用 FTS5 trigram 与重要度时间衰减排序。
pkg/hindsight 是 memoryflow 的召回策略插件。pkg/semantic-router 提供
检索前的意图路由。

## 核心功能

CortexDB 提供向量检索、混合检索与词法检索三种模式。SaveKnowledge 保存
持久化知识并自动分块、建立文档节点与实体节点。SearchKnowledge 执行
GraphRAG 检索，结合向量召回与图扩展。SaveMemory 与 SearchMemory 提供
按用户、会话作用域隔离的代理记忆。知识图谱 API 支持 RDF 三元组的
增删查改、SPARQL SELECT 与 ASK 查询、Turtle 与 N-Triples 导入导出、
RDFS-lite 推理刷新以及 SHACL-lite 形状校验。本体 API 用 OntologySchema
声明实体类型与关系类型来约束图谱写入。无嵌入模式是一等公民：
没有嵌入模型时所有检索 API 自动退化为词法检索。

## 工具与 MCP

GraphRAGTools 返回进程内工具箱，Call 方法按名称分发工具调用。
NewMCPServer 与 RunMCPStdio 把同一套工具暴露为 MCP 服务，
供 Claude 等代理直接调用。工具包括 ingest_document、search_text、
expand_graph、build_context、knowledge_save、knowledge_search、
memory_save、memory_search、knowledge_graph_query 与 ontology_save。
memoryflow 与 graphflow 另有独立的工具箱与 MCP 入口。
cortexdb-mcp-stdio 是 MCP 标准输入输出服务的二进制入口。

## gRPC 与 Rust 客户端

cortexdb-grpc 是 gRPC 边车二进制，把完整门面 API 通过 cortexdb.v1
协议暴露给非 Go 语言。协议包含 KnowledgeService、MemoryService、
KnowledgeGraphService、GraphRagService、ToolsService 与 AdminService。
CORTEXDB_GRPC_TOKEN 启用 Bearer Token 认证。OPENAI_BASE_URL 指向
任意 OpenAI 兼容端点即可启用向量模式，例如本地 Ollama 的
embeddinggemma 模型。cortexdb-client 是发布在 crates.io 的 Rust crate，
通过 CortexClient 提供类型化访问，managed-server 特性可自动下载
并拉起边车进程。

## 常用命令与示例

go build 编译全部代码。go test -race 运行完整测试套件。
examples 目录包含九个可运行示例：01_core 演示向量检索，
02_rag 演示 RAG 知识管理，03_memoryflow 演示对话记忆工作流，
04_knowledge_graph 演示 SPARQL 与推理，05_graphflow 演示语料建图，
06_tools_mcp 演示工具调用，07_importflow 演示结构化数据导入，
08_self_knowledge_graph 演示把项目文档建成知识图谱并做图谱问答，
kg_e2e 演示端到端知识图谱流程。scripts/gen-proto.sh 重新生成
gRPC 代码。BenchmarkRAG 与 BenchmarkKnowledgeGraph 是 README
引用的性能基准。
