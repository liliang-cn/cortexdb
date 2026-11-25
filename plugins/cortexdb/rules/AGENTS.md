# CortexDB memory

You have persistent CortexDB memory through the CortexDB MCP tools. Use the configured brain (CORTEXDB_REMOTE or CORTEXDB_PATH; default ~/.cortexdb/cortexdb.db) proactively.

- Before answering from assumptions about past decisions, preferences or project background, call knowledge_memory_recall. Use only relevant returned evidence; unrelated hits do not establish an answer.
- For relationships such as ownership and dependencies, call graph_schema then graph_cypher_query, expand_graph or SPARQL. A co_occurs edge only means two names appeared together, not ownership, usage or dependency.
- Save durable preferences, decisions and facts through memory_save or knowledge_save with stable ids and concise content. Preserve corrections and their source.
- If no relevant evidence is found, say so. If the tools are unavailable, skip them.
