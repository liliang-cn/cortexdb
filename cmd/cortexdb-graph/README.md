# SQLite Vector Graph CLI

A command-line interface for managing nodes, edges, and performing searches in the SQLite vector graph store.

## Installation

```bash
go install ./cmd/cortexdb-graph
```

## Usage

### Initialize Database

```bash
# Create a new graph database
cortexdb-graph init --db mydata.db --dimensions 384

# With HNSW index enabled
cortexdb-graph init --db mydata.db --dimensions 384 --enable-hnsw
```

### Node Management

```bash
# Add a node
cortexdb-graph node add "node1" \
  --content "This is a sample node" \
  --type "document" \
  --vector "0.1,0.2,0.3" \
  --properties '{"category":"tech","priority":1}'

# Get node details
cortexdb-graph node get "node1"
cortexdb-graph node get "node1" --json

# List all nodes
cortexdb-graph node list
cortexdb-graph node list --type "document" --limit 10

# Delete a node
cortexdb-graph node delete "node1"
```

### Edge Management

```bash
# Add an edge
cortexdb-graph edge add "edge1" "node1" "node2" \
  --type "references" \
  --weight 0.8 \
  --properties '{"strength":"strong"}'

# List edges for a node
cortexdb-graph edge list "node1" --direction out
cortexdb-graph edge list "node1" --direction in
cortexdb-graph edge list "node1" --direction both
```

### Search Operations

```bash
# Vector similarity search
cortexdb-graph search vector \
  --vector "0.1,0.2,0.3" \
  --top-k 5 \
  --threshold 0.7

# Use HNSW index for faster search
cortexdb-graph search vector \
  --vector "0.1,0.2,0.3" \
  --top-k 5 \
  --hnsw

# Hybrid search (vector + graph)
cortexdb-graph search hybrid \
  --vector "0.1,0.2,0.3" \
  --start-node "node1" \
  --top-k 10 \
  --vector-weight 0.5 \
  --graph-weight 0.3 \
  --edge-weight 0.2 \
  --max-depth 3
```

### Import/Export

```bash
# Export to different formats
cortexdb-graph export graphml graph.graphml
cortexdb-graph export gexf graph.gexf
cortexdb-graph export json graph.json

# Import from files
cortexdb-graph import graphml graph.graphml
cortexdb-graph import gexf graph.gexf
cortexdb-graph import json graph.json
```

### Statistics

```bash
# Display graph statistics
cortexdb-graph stats
cortexdb-graph stats --json
```

## Global Options

- `--db, -d`: Database file path (default: "graph.db")
- `--dimensions, -n`: Vector dimensions (0 for automatic)
- `--verbose, -v`: Verbose output

## Examples

### Building a Knowledge Graph

```bash
# Initialize database
cortexdb-graph init --db knowledge.db --dimensions 384 --enable-hnsw

# Add documents as nodes
cortexdb-graph node add "doc1" --type "document" --content "Machine learning basics"
cortexdb-graph node add "doc2" --type "document" --content "Deep learning introduction"
cortexdb-graph node add "doc3" --type "document" --content "Neural networks explained"

# Create relationships
cortexdb-graph edge add "rel1" "doc1" "doc2" --type "prerequisite" --weight 0.9
cortexdb-graph edge add "rel2" "doc2" "doc3" --type "related" --weight 0.7

# Search for similar documents
cortexdb-graph search vector --vector "0.1,0.2,0.3" --top-k 5

# Find related documents through graph traversal
cortexdb-graph search hybrid --start-node "doc1" --max-depth 2
```

### Recommendation System

```bash
# Add users and items
cortexdb-graph node add "user1" --type "user" --properties '{"name":"Alice"}'
cortexdb-graph node add "item1" --type "product" --content "Laptop"
cortexdb-graph node add "item2" --type "product" --content "Mouse"

# Add interactions
cortexdb-graph edge add "purchase1" "user1" "item1" --type "purchased" --weight 1.0
cortexdb-graph edge add "view1" "user1" "item2" --type "viewed" --weight 0.5

# Find recommendations
cortexdb-graph search hybrid --start-node "user1" --graph-weight 0.7 --max-depth 2
```

## Output Formats

Most commands support JSON output with the `--json` flag for programmatic processing:

```bash
cortexdb-graph node get "node1" --json | jq '.properties'
cortexdb-graph stats --json | jq '.TotalNodes'
```