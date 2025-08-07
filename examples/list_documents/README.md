# List Documents Example

This example demonstrates how to use the `ListDocuments` method to retrieve all unique document IDs from the vector store.

## Features Demonstrated

- **ListDocuments()**: Returns all unique document IDs in the store
- **Sorted Results**: Document IDs are returned in alphabetical order
- **Filtering**: Only non-empty document IDs are included
- **Real-time Updates**: The list reflects current state after deletions

## Usage

```go
// List all documents
docIDs, err := store.ListDocuments(ctx)
if err != nil {
    log.Fatalf("Failed to list documents: %v", err)
}

fmt.Printf("Found %d unique documents:\n", len(docIDs))
for i, docID := range docIDs {
    fmt.Printf("%d. %s\n", i+1, docID)
}
```

## Run the Example

```bash
go run main.go
```

## Expected Output

The example will:

1. Insert sample embeddings with various document IDs
2. List all unique documents (should show 3 documents)
3. Delete one document and list again (should show 2 documents)
4. Display store statistics before and after deletion

This is useful for:

- Discovering what documents are stored in your vector database
- Managing document collections
- Cleanup and maintenance operations
- Building document browsing interfaces
