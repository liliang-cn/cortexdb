# SQLite Vector Graph CLI

A command-line interface for managing nodes, edges, and performing searches in the SQLite vector graph store.

## Installation

```bash
go install ./cmd/sqvect-graph
```

## Usage

### Initialize Database

```bash
# Create a new graph database
sqvect-graph init --db mydata.db --dimensions 384

# With HNSW index enabled
sqvect-graph init --db mydata.db --dimensions 384 --enable-hnsw
```

### Node Management

```bash
# Add a node
sqvect-graph node add "node1" \
  --content "This is a sample node" \
  --type "document" \
  --vector "0.1,0.2,0.3" \
  --properties '{"category":"tech","priority":1}'

# Get node details
sqvect-graph node get "node1"
sqvect-graph node get "node1" --json

# List all nodes
sqvect-graph node list
sqvect-graph node list --type "document" --limit 10

# Delete a node
sqvect-graph node delete "node1"
```

### Edge Management

```bash
# Add an edge
sqvect-graph edge add "edge1" "node1" "node2" \
  --type "references" \
  --weight 0.8 \
  --properties '{"strength":"strong"}'

# List edges for a node
sqvect-graph edge list "node1" --direction out
sqvect-graph edge list "node1" --direction in
sqvect-graph edge list "node1" --direction both
```

### Search Operations

```bash
# Vector similarity search
sqvect-graph search vector \
  --vector "0.1,0.2,0.3" \
  --top-k 5 \
  --threshold 0.7

# Use HNSW index for faster search
sqvect-graph search vector \
  --vector "0.1,0.2,0.3" \
  --top-k 5 \
  --hnsw

# Hybrid search (vector + graph)
sqvect-graph search hybrid \
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
sqvect-graph export graphml graph.graphml
sqvect-graph export gexf graph.gexf
sqvect-graph export json graph.json

# Import from files
sqvect-graph import graphml graph.graphml
sqvect-graph import gexf graph.gexf
sqvect-graph import json graph.json
```

### Statistics

```bash
# Display graph statistics
sqvect-graph stats
sqvect-graph stats --json
```

## Global Options

- `--db, -d`: Database file path (default: "graph.db")
- `--dimensions, -n`: Vector dimensions (0 for automatic)
- `--verbose, -v`: Verbose output

## Examples

### Building a Knowledge Graph

```bash
# Initialize database
sqvect-graph init --db knowledge.db --dimensions 384 --enable-hnsw

# Add documents as nodes
sqvect-graph node add "doc1" --type "document" --content "Machine learning basics"
sqvect-graph node add "doc2" --type "document" --content "Deep learning introduction"
sqvect-graph node add "doc3" --type "document" --content "Neural networks explained"

# Create relationships
sqvect-graph edge add "rel1" "doc1" "doc2" --type "prerequisite" --weight 0.9
sqvect-graph edge add "rel2" "doc2" "doc3" --type "related" --weight 0.7

# Search for similar documents
sqvect-graph search vector --vector "0.1,0.2,0.3" --top-k 5

# Find related documents through graph traversal
sqvect-graph search hybrid --start-node "doc1" --max-depth 2
```

### Recommendation System

```bash
# Add users and items
sqvect-graph node add "user1" --type "user" --properties '{"name":"Alice"}'
sqvect-graph node add "item1" --type "product" --content "Laptop"
sqvect-graph node add "item2" --type "product" --content "Mouse"

# Add interactions
sqvect-graph edge add "purchase1" "user1" "item1" --type "purchased" --weight 1.0
sqvect-graph edge add "view1" "user1" "item2" --type "viewed" --weight 0.5

# Find recommendations
sqvect-graph search hybrid --start-node "user1" --graph-weight 0.7 --max-depth 2
```

## Output Formats

Most commands support JSON output with the `--json` flag for programmatic processing:

```bash
sqvect-graph node get "node1" --json | jq '.properties'
sqvect-graph stats --json | jq '.TotalNodes'
```