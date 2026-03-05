# SQLite Vector Store CLI

A command-line interface for managing vector embeddings in SQLite database.

## Installation

```bash
go install github.com/liliang-cn/cortexdb/cmd/cortexdb
```

## Usage

### Initialize Database

```bash
# Create a new vector database
cortexdb init --db vectors.db --dimensions 384

# Auto-detect dimensions (from first insert)
cortexdb init --db vectors.db --dimensions 0
```

### Embedding Management

```bash
# Add an embedding
cortexdb embed add "emb1" \
  --vector "0.1,0.2,0.3" \
  --content "Sample text" \
  --doc-id "doc1" \
  --metadata '{"category":"example","lang":"en"}'

# Get embedding details
cortexdb embed get "emb1"
cortexdb embed get "emb1" --json

# Delete an embedding
cortexdb embed delete "emb1"

# Batch import from JSON file
cortexdb embed batch embeddings.json
```

Example JSON file for batch import:
```json
[
  {
    "id": "emb1",
    "vector": [0.1, 0.2, 0.3],
    "content": "First embedding",
    "doc_id": "doc1",
    "metadata": {"type": "example"}
  },
  {
    "id": "emb2",
    "vector": [0.4, 0.5, 0.6],
    "content": "Second embedding",
    "doc_id": "doc2",
    "metadata": {"type": "test"}
  }
]
```

### Vector Search

```bash
# Basic similarity search
cortexdb search --vector "0.1,0.2,0.3" --top-k 5

# Search with threshold
cortexdb search --vector "0.1,0.2,0.3" --top-k 10 --threshold 0.8

# Search with metadata filters
cortexdb search --vector "0.1,0.2,0.3" \
  --filter "category=example,lang=en"

# Output as JSON
cortexdb search --vector "0.1,0.2,0.3" --json
```

### Collection Management

```bash
# Create a collection
cortexdb collection create "documents" --dimensions 768

# List all collections
cortexdb collection list
cortexdb collection list --json

# Delete a collection
cortexdb collection delete "documents" --force
```

### Database Operations

```bash
# Display statistics
cortexdb stats
cortexdb stats --json

# Optimize database (VACUUM and ANALYZE)
cortexdb optimize
```

### Similarity Calculation

```bash
# Calculate cosine similarity
cortexdb similarity \
  --vector1 "0.1,0.2,0.3" \
  --vector2 "0.4,0.5,0.6" \
  --method cosine

# Other methods: dot, euclidean
cortexdb similarity \
  --vector1 "0.1,0.2,0.3" \
  --vector2 "0.4,0.5,0.6" \
  --method euclidean
```

## Global Options

- `--db, -d`: Database file path (default: "vectors.db")
- `--dimensions, -n`: Vector dimensions (0 for automatic)
- `--verbose, -v`: Verbose output

## Examples

### Text Embedding Workflow

```bash
# Initialize database
cortexdb init --db documents.db --dimensions 384

# Add document embeddings
cortexdb embed add "doc001" \
  --vector "$(python generate_embedding.py 'Machine learning basics')" \
  --content "Machine learning basics" \
  --metadata '{"topic":"ML","level":"beginner"}'

cortexdb embed add "doc002" \
  --vector "$(python generate_embedding.py 'Deep learning introduction')" \
  --content "Deep learning introduction" \
  --metadata '{"topic":"DL","level":"intermediate"}'

# Search for similar documents
cortexdb search \
  --vector "$(python generate_embedding.py 'Neural networks')" \
  --top-k 5 \
  --verbose
```

### Image Embedding Workflow

```bash
# Create image collection
cortexdb collection create "images" --dimensions 512

# Add image embeddings
cortexdb embed add "img_001" \
  --vector "$(python extract_image_features.py image1.jpg)" \
  --content "sunset_beach.jpg" \
  --metadata '{"type":"landscape","location":"hawaii"}'

# Find similar images
cortexdb search \
  --vector "$(python extract_image_features.py query.jpg)" \
  --filter "type=landscape" \
  --top-k 10
```

### Performance Testing

```bash
# Generate test data
for i in {1..1000}; do
  vector=$(python -c "import random; print(','.join(str(random.random()) for _ in range(384)))")
  cortexdb embed add "test_$i" --vector "$vector" --content "Test $i"
done

# Test search performance
time cortexdb search --vector "$(python -c "import random; print(','.join(str(random.random()) for _ in range(384)))")" --top-k 100

# Check database stats
cortexdb stats

# Optimize for better performance
cortexdb optimize
```

## Output Formats

Most commands support JSON output with the `--json` flag for programmatic processing:

```bash
# Pipe to jq for processing
cortexdb search --vector "0.1,0.2,0.3" --json | jq '.[] | .id'

# Get embedding count
cortexdb stats --json | jq '.Count'

# List collection dimensions
cortexdb collection list --json | jq '.[] | "\(.Name): \(.Dimensions)"'
```

## Similarity Methods

- **cosine**: Cosine similarity (range: -1 to 1, higher is more similar)
- **dot**: Dot product similarity (unbounded, higher is more similar)
- **euclidean**: Euclidean distance (lower is more similar)

Choose based on your use case:
- Use **cosine** for normalized vectors (most common for text embeddings)
- Use **dot** for non-normalized vectors where magnitude matters
- Use **euclidean** for spatial/geometric similarity