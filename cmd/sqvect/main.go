package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/liliang-cn/sqvect/pkg/core"
)

var (
	dbPath     string
	dimensions int
	verbose    bool
)

var rootCmd = &cobra.Command{
	Use:   "sqvect",
	Short: "CLI tool for SQLite vector storage",
	Long:  `A command-line interface for managing vector embeddings in SQLite database.`,
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new vector database",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := core.New(dbPath, dimensions)
		if err != nil {
			return fmt.Errorf("failed to create store: %w", err)
		}
		defer store.Close()

		ctx := context.Background()
		if err := store.Init(ctx); err != nil {
			return fmt.Errorf("failed to initialize store: %w", err)
		}

		fmt.Printf("Vector database initialized at %s with %d dimensions\n", dbPath, dimensions)
		return nil
	},
}

var embedCmd = &cobra.Command{
	Use:   "embed",
	Short: "Manage embeddings",
}

var embedAddCmd = &cobra.Command{
	Use:   "add <id>",
	Short: "Add or update an embedding",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		
		content, _ := cmd.Flags().GetString("content")
		vectorStr, _ := cmd.Flags().GetString("vector")
		docID, _ := cmd.Flags().GetString("doc-id")
		metadataStr, _ := cmd.Flags().GetString("metadata")
		collection, _ := cmd.Flags().GetString("collection")
		
		// Parse vector
		var vector []float32
		if vectorStr != "" {
			parts := strings.Split(vectorStr, ",")
			for _, part := range parts {
				val, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
				if err != nil {
					return fmt.Errorf("invalid vector format: %w", err)
				}
				vector = append(vector, float32(val))
			}
		} else {
			return fmt.Errorf("vector is required")
		}
		
		// Parse metadata
		metadata := make(map[string]string)
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
				return fmt.Errorf("invalid metadata JSON: %w", err)
			}
		}
		
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		embedding := &core.Embedding{
			ID:         id,
			Collection: collection,
			Vector:     vector,
			Content:    content,
			DocID:      docID,
			Metadata:   metadata,
		}
		
		ctx := context.Background()
		if err := store.Upsert(ctx, embedding); err != nil {
			return fmt.Errorf("failed to add embedding: %w", err)
		}
		
		fmt.Printf("Embedding '%s' added successfully\n", id)
		return nil
	},
}

var embedGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get an embedding by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		// Direct Get method not available in core
		// Would need to query database directly
		db := store.GetDB()
		query := `SELECT id, vector, content, doc_id, metadata FROM embeddings WHERE id = ?`
		
		var vectorBytes []byte
		var content, docID, metadataJSON string
		err = db.QueryRowContext(ctx, query, id).Scan(&id, &vectorBytes, &content, &docID, &metadataJSON)
		if err != nil {
			return fmt.Errorf("failed to get embedding: %w", err)
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			result := map[string]interface{}{
				"id": id,
				"content": content,
				"doc_id": docID,
				"metadata": metadataJSON,
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("ID: %s\n", id)
			fmt.Printf("Content: %s\n", content)
			fmt.Printf("Doc ID: %s\n", docID)
			fmt.Printf("Metadata: %s\n", metadataJSON)
		}
		
		return nil
	},
}

var embedDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete an embedding",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		if err := store.Delete(ctx, id); err != nil {
			return fmt.Errorf("failed to delete embedding: %w", err)
		}
		
		fmt.Printf("Embedding '%s' deleted successfully\n", id)
		return nil
	},
}

var embedBatchCmd = &cobra.Command{
	Use:   "batch <json-file>",
	Short: "Add embeddings in batch from JSON file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filename := args[0]
		
		data, err := os.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		
		var embeddings []*core.Embedding
		if err := json.Unmarshal(data, &embeddings); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}
		
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		if err := store.UpsertBatch(ctx, embeddings); err != nil {
			return fmt.Errorf("batch insert failed: %w", err)
		}
		
		fmt.Printf("Successfully added %d embeddings\n", len(embeddings))
		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search for similar vectors",
	RunE: func(cmd *cobra.Command, args []string) error {
		vectorStr, _ := cmd.Flags().GetString("vector")
		k, _ := cmd.Flags().GetInt("top-k")
		threshold, _ := cmd.Flags().GetFloat64("threshold")
		filter, _ := cmd.Flags().GetString("filter")
		collection, _ := cmd.Flags().GetString("collection")
		
		// Parse vector
		var vector []float32
		parts := strings.Split(vectorStr, ",")
		for _, part := range parts {
			val, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
			if err != nil {
				return fmt.Errorf("invalid vector format: %w", err)
			}
			vector = append(vector, float32(val))
		}
		
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		opts := core.SearchOptions{
			Collection: collection,
			TopK:       k,
			Threshold:  threshold,
		}
		
		if filter != "" {
			// Parse filter as key=value pairs
			filters := make(map[string]string)
			pairs := strings.Split(filter, ",")
			for _, pair := range pairs {
				kv := strings.Split(pair, "=")
				if len(kv) == 2 {
					filters[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
				}
			}
			opts.Filter = filters
		}
		
		ctx := context.Background()
		results, err := store.Search(ctx, vector, opts)
		if err != nil {
			return fmt.Errorf("search failed: %w", err)
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			data, _ := json.MarshalIndent(results, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("Found %d results:\n", len(results))
			for i, result := range results {
				fmt.Printf("%d. %s (score: %.4f)\n", i+1, result.ID, result.Score)
				if verbose && result.Content != "" {
					fmt.Printf("   Content: %s\n", result.Content)
				}
			}
		}
		
		return nil
	},
}

// Collection commands removed - not implemented in core

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Display database statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		stats, err := store.Stats(ctx)
		if err != nil {
			return fmt.Errorf("failed to get stats: %w", err)
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			data, _ := json.MarshalIndent(stats, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Println("Database Statistics:")
			fmt.Printf("  Total Embeddings: %d\n", stats.Count)
			fmt.Printf("  Vector Dimensions: %d\n", stats.Dimensions)
			fmt.Printf("  Database Size: %.2f MB\n", float64(stats.Size)/(1024*1024))
		}
		
		return nil
	},
}

var optimizeCmd = &cobra.Command{
	Use:   "optimize",
	Short: "Optimize the database",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		
		fmt.Println("Optimizing database...")
		
		// Run VACUUM to reclaim space
		if _, err := store.GetDB().ExecContext(ctx, "VACUUM"); err != nil {
			return fmt.Errorf("vacuum failed: %w", err)
		}
		
		// Run ANALYZE to update statistics
		if _, err := store.GetDB().ExecContext(ctx, "ANALYZE"); err != nil {
			return fmt.Errorf("analyze failed: %w", err)
		}
		
		fmt.Println("Database optimized successfully")
		return nil
	},
}

var similarityCmd = &cobra.Command{
	Use:   "similarity",
	Short: "Calculate similarity between two vectors",
	RunE: func(cmd *cobra.Command, args []string) error {
		vector1Str, _ := cmd.Flags().GetString("vector1")
		vector2Str, _ := cmd.Flags().GetString("vector2")
		method, _ := cmd.Flags().GetString("method")
		
		// Parse vectors
		vector1 := parseVector(vector1Str)
		vector2 := parseVector(vector2Str)
		
		if len(vector1) != len(vector2) {
			return fmt.Errorf("vectors must have the same dimensions")
		}
		
		var score float64
		switch method {
		case "cosine":
			score = core.CosineSimilarity(vector1, vector2)
		case "dot":
			similarityFunc := core.GetDotProduct()
			score = similarityFunc(vector1, vector2)
		case "euclidean":
			similarityFunc := core.GetEuclideanDist()
			score = similarityFunc(vector1, vector2)
		default:
			return fmt.Errorf("unknown similarity method: %s", method)
		}
		
		fmt.Printf("Similarity (%s): %.6f\n", method, score)
		return nil
	},
}

var collectionCmd = &cobra.Command{
	Use:   "collection",
	Short: "Manage collections",
}

var collectionCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new collection",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		dimensions, _ := cmd.Flags().GetInt("dimensions")
		
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		collection, err := store.CreateCollection(ctx, name, dimensions)
		if err != nil {
			return fmt.Errorf("failed to create collection: %w", err)
		}
		
		fmt.Printf("Collection '%s' created with %d dimensions\n", collection.Name, collection.Dimensions)
		return nil
	},
}

var collectionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all collections",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		collections, err := store.ListCollections(ctx)
		if err != nil {
			return fmt.Errorf("failed to list collections: %w", err)
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			data, _ := json.MarshalIndent(collections, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("Collections (%d):\n", len(collections))
			for _, col := range collections {
				fmt.Printf("  %s (dimensions: %d, created: %s)\n", 
					col.Name, col.Dimensions, col.CreatedAt.Format("2006-01-02 15:04"))
			}
		}
		return nil
	},
}

var collectionDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a collection",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		force, _ := cmd.Flags().GetBool("force")
		
		if !force {
			fmt.Printf("Are you sure you want to delete collection '%s'? This will delete all embeddings in it. [y/N]: ", name)
			var response string
			fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}
		
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		if err := store.DeleteCollection(ctx, name); err != nil {
			return fmt.Errorf("failed to delete collection: %w", err)
		}
		
		fmt.Printf("Collection '%s' deleted successfully\n", name)
		return nil
	},
}

var collectionStatsCmd = &cobra.Command{
	Use:   "stats <name>",
	Short: "Display collection statistics",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		
		store, err := openStore()
		if err != nil {
			return err
		}
		defer store.Close()
		
		ctx := context.Background()
		stats, err := store.GetCollectionStats(ctx, name)
		if err != nil {
			return fmt.Errorf("failed to get collection stats: %w", err)
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			data, _ := json.MarshalIndent(stats, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("Collection: %s\n", stats.Name)
			fmt.Printf("  Embeddings: %d\n", stats.Count)
			fmt.Printf("  Dimensions: %d\n", stats.Dimensions)
			fmt.Printf("  Size: %.2f MB\n", float64(stats.Size)/(1024*1024))
			fmt.Printf("  Created: %s\n", stats.CreatedAt.Format("2006-01-02 15:04:05"))
			if !stats.LastInsertedAt.IsZero() {
				fmt.Printf("  Last Insert: %s\n", stats.LastInsertedAt.Format("2006-01-02 15:04:05"))
			}
		}
		return nil
	},
}

func parseVector(str string) []float32 {
	var vector []float32
	parts := strings.Split(str, ",")
	for _, part := range parts {
		val, _ := strconv.ParseFloat(strings.TrimSpace(part), 32)
		vector = append(vector, float32(val))
	}
	return vector
}

func openStore() (*core.SQLiteStore, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("database path not specified")
	}
	
	store, err := core.New(dbPath, dimensions)
	if err != nil {
		return nil, fmt.Errorf("failed to open store: %w", err)
	}
	
	// Initialize the store if not already done
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		store.Close()
		return nil, fmt.Errorf("failed to initialize store: %w", err)
	}
	
	return store, nil
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVarP(&dbPath, "db", "d", "vectors.db", "Database file path")
	rootCmd.PersistentFlags().IntVarP(&dimensions, "dimensions", "n", 0, "Vector dimensions (0 for auto)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	
	// Embed commands
	embedCmd.AddCommand(embedAddCmd, embedGetCmd, embedDeleteCmd, embedBatchCmd)
	embedAddCmd.Flags().String("content", "", "Embedding content")
	embedAddCmd.Flags().String("vector", "", "Vector values (comma-separated)")
	embedAddCmd.Flags().String("doc-id", "", "Document ID")
	embedAddCmd.Flags().String("metadata", "", "Metadata as JSON")
	embedAddCmd.Flags().String("collection", "", "Collection name")
	embedAddCmd.MarkFlagRequired("vector")
	
	embedGetCmd.Flags().Bool("json", false, "Output as JSON")
	
	// Search command
	searchCmd.Flags().String("vector", "", "Query vector (comma-separated)")
	searchCmd.Flags().Int("top-k", 10, "Number of results")
	searchCmd.Flags().Float64("threshold", 0.0, "Similarity threshold")
	searchCmd.Flags().String("filter", "", "Metadata filters (key=value,key2=value2)")
	searchCmd.Flags().String("collection", "", "Collection to search in")
	searchCmd.Flags().Bool("json", false, "Output as JSON")
	searchCmd.MarkFlagRequired("vector")
	
	// Collection commands
	collectionCmd.AddCommand(collectionCreateCmd, collectionListCmd, collectionDeleteCmd, collectionStatsCmd)
	collectionCreateCmd.Flags().Int("dimensions", 0, "Vector dimensions (0 for auto)")
	collectionListCmd.Flags().Bool("json", false, "Output as JSON")
	collectionDeleteCmd.Flags().Bool("force", false, "Skip confirmation prompt")
	collectionStatsCmd.Flags().Bool("json", false, "Output as JSON")
	
	// Similarity command
	similarityCmd.Flags().String("vector1", "", "First vector (comma-separated)")
	similarityCmd.Flags().String("vector2", "", "Second vector (comma-separated)")
	similarityCmd.Flags().String("method", "cosine", "Similarity method (cosine/dot/euclidean)")
	similarityCmd.MarkFlagRequired("vector1")
	similarityCmd.MarkFlagRequired("vector2")
	
	// Stats command
	statsCmd.Flags().Bool("json", false, "Output as JSON")
	
	// Add all commands to root
	rootCmd.AddCommand(
		initCmd,
		embedCmd,
		searchCmd,
		collectionCmd,
		statsCmd,
		optimizeCmd,
		similarityCmd,
	)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}