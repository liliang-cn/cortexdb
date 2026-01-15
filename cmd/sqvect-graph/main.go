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
	"github.com/liliang-cn/sqvect/v2/pkg/core"
	graphpkg "github.com/liliang-cn/sqvect/v2/pkg/graph"
)

var (
	dbPath     string
	dimensions int
	verbose    bool
)

var rootCmd = &cobra.Command{
	Use:   "sqvect-graph",
	Short: "CLI tool for managing SQLite vector graph database",
	Long:  `A command-line interface for managing nodes, edges, and performing searches in the SQLite vector graph store.`,
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new graph database",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := core.New(dbPath, dimensions)
		if err != nil {
			return fmt.Errorf("failed to create store: %w", err)
		}
		defer func() { _ = store.Close() }()

		ctx := context.Background()
		if err := store.Init(ctx); err != nil {
			return fmt.Errorf("failed to initialize store: %w", err)
		}

		graph := graphpkg.NewGraphStore(store)
		if err := graph.InitGraphSchema(ctx); err != nil {
			return fmt.Errorf("failed to initialize graph schema: %w", err)
		}

		enableHNSW, _ := cmd.Flags().GetBool("enable-hnsw")
		if enableHNSW {
			if err := graph.EnableHNSWIndex(dimensions); err != nil {
				return fmt.Errorf("failed to enable HNSW index: %w", err)
			}
			fmt.Println("HNSW index enabled")
		}

		fmt.Printf("Graph database initialized at %s with %d dimensions\n", dbPath, dimensions)
		return nil
	},
}

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Manage graph nodes",
}

var nodeAddCmd = &cobra.Command{
	Use:   "add <id>",
	Short: "Add or update a node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID := args[0]
		
		content, _ := cmd.Flags().GetString("content")
		nodeType, _ := cmd.Flags().GetString("type")
		vectorStr, _ := cmd.Flags().GetString("vector")
		propsStr, _ := cmd.Flags().GetString("properties")
		
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
		}
		
		// Parse properties
		var properties map[string]interface{}
		if propsStr != "" {
			if err := json.Unmarshal([]byte(propsStr), &properties); err != nil {
				return fmt.Errorf("invalid properties JSON: %w", err)
			}
		}
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		node := &graphpkg.GraphNode{
			ID:         nodeID,
			Vector:     vector,
			Content:    content,
			NodeType:   nodeType,
			Properties: properties,
		}
		
		ctx := context.Background()
		if err := graph.UpsertNode(ctx, node); err != nil {
			return fmt.Errorf("failed to add node: %w", err)
		}
		
		fmt.Printf("Node '%s' added successfully\n", nodeID)
		return nil
	},
}

var nodeGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get a node by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID := args[0]
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		ctx := context.Background()
		node, err := graph.GetNode(ctx, nodeID)
		if err != nil {
			return fmt.Errorf("failed to get node: %w", err)
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			data, _ := json.MarshalIndent(node, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("Node ID: %s\n", node.ID)
			fmt.Printf("Type: %s\n", node.NodeType)
			fmt.Printf("Content: %s\n", node.Content)
			fmt.Printf("Vector: %v\n", node.Vector)
			if len(node.Properties) > 0 {
				fmt.Printf("Properties: %v\n", node.Properties)
			}
			fmt.Printf("Created: %s\n", node.CreatedAt)
			fmt.Printf("Updated: %s\n", node.UpdatedAt)
		}
		
		return nil
	},
}

var nodeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all nodes",
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeType, _ := cmd.Flags().GetString("type")
		limit, _ := cmd.Flags().GetInt("limit")
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		ctx := context.Background()
		
		var filter *graphpkg.GraphFilter
		if nodeType != "" {
			filter = &graphpkg.GraphFilter{
				NodeTypes: []string{nodeType},
			}
		}
		
		nodes, err := graph.GetAllNodes(ctx, filter)
		if err != nil {
			return fmt.Errorf("failed to list nodes: %w", err)
		}
		
		if limit > 0 && len(nodes) > limit {
			nodes = nodes[:limit]
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			data, _ := json.MarshalIndent(nodes, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("Found %d nodes\n", len(nodes))
			for _, node := range nodes {
				fmt.Printf("- %s (type: %s)\n", node.ID, node.NodeType)
			}
		}
		
		return nil
	},
}

var nodeDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID := args[0]
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		ctx := context.Background()
		if err := graph.DeleteNode(ctx, nodeID); err != nil {
			return fmt.Errorf("failed to delete node: %w", err)
		}
		
		fmt.Printf("Node '%s' deleted successfully\n", nodeID)
		return nil
	},
}

var edgeCmd = &cobra.Command{
	Use:   "edge",
	Short: "Manage graph edges",
}

var edgeAddCmd = &cobra.Command{
	Use:   "add <id> <from-node> <to-node>",
	Short: "Add or update an edge",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		edgeID := args[0]
		fromNodeID := args[1]
		toNodeID := args[2]
		
		edgeType, _ := cmd.Flags().GetString("type")
		weight, _ := cmd.Flags().GetFloat64("weight")
		propsStr, _ := cmd.Flags().GetString("properties")
		
		// Parse properties
		var properties map[string]interface{}
		if propsStr != "" {
			if err := json.Unmarshal([]byte(propsStr), &properties); err != nil {
				return fmt.Errorf("invalid properties JSON: %w", err)
			}
		}
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		edge := &graphpkg.GraphEdge{
			ID:         edgeID,
			FromNodeID: fromNodeID,
			ToNodeID:   toNodeID,
			EdgeType:   edgeType,
			Weight:     weight,
			Properties: properties,
		}
		
		ctx := context.Background()
		if err := graph.UpsertEdge(ctx, edge); err != nil {
			return fmt.Errorf("failed to add edge: %w", err)
		}
		
		fmt.Printf("Edge '%s' added successfully\n", edgeID)
		return nil
	},
}

var edgeListCmd = &cobra.Command{
	Use:   "list <node-id>",
	Short: "List edges for a node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeID := args[0]
		direction, _ := cmd.Flags().GetString("direction")
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		ctx := context.Background()
		edges, err := graph.GetEdges(ctx, nodeID, direction)
		if err != nil {
			return fmt.Errorf("failed to list edges: %w", err)
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			data, _ := json.MarshalIndent(edges, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("Found %d edges\n", len(edges))
			for _, edge := range edges {
				fmt.Printf("- %s: %s -> %s (type: %s, weight: %.2f)\n",
					edge.ID, edge.FromNodeID, edge.ToNodeID, edge.EdgeType, edge.Weight)
			}
		}
		
		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search the graph",
}

var searchVectorCmd = &cobra.Command{
	Use:   "vector",
	Short: "Vector similarity search",
	RunE: func(cmd *cobra.Command, args []string) error {
		vectorStr, _ := cmd.Flags().GetString("vector")
		k, _ := cmd.Flags().GetInt("top-k")
		threshold, _ := cmd.Flags().GetFloat64("threshold")
		useHNSW, _ := cmd.Flags().GetBool("hnsw")
		
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
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		ctx := context.Background()
		
		if useHNSW {
			results, err := graph.HNSWSearch(ctx, vector, k, threshold)
			if err != nil {
				return fmt.Errorf("HNSW search failed: %w", err)
			}
			
			fmt.Printf("Found %d results:\n", len(results))
			for i, result := range results {
				fmt.Printf("%d. %s (score: %.4f)\n", i+1, result.Node.ID, result.VectorScore)
			}
		} else {
			// Use regular vector search from store
			results, err := store.Search(ctx, vector, core.SearchOptions{TopK: k})
			if err != nil {
				return fmt.Errorf("search failed: %w", err)
			}
			
			fmt.Printf("Found %d results:\n", len(results))
			for i, result := range results {
				fmt.Printf("%d. %s (score: %.4f)\n", i+1, result.ID, result.Score)
			}
		}
		
		return nil
	},
}

var searchHybridCmd = &cobra.Command{
	Use:   "hybrid",
	Short: "Hybrid vector and graph search",
	RunE: func(cmd *cobra.Command, args []string) error {
		vectorStr, _ := cmd.Flags().GetString("vector")
		startNode, _ := cmd.Flags().GetString("start-node")
		k, _ := cmd.Flags().GetInt("top-k")
		threshold, _ := cmd.Flags().GetFloat64("threshold")
		vectorWeight, _ := cmd.Flags().GetFloat64("vector-weight")
		graphWeight, _ := cmd.Flags().GetFloat64("graph-weight")
		edgeWeight, _ := cmd.Flags().GetFloat64("edge-weight")
		maxDepth, _ := cmd.Flags().GetInt("max-depth")
		
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
		}
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		query := &graphpkg.HybridQuery{
			Vector:      vector,
			StartNodeID: startNode,
			TopK:        k,
			Threshold:   threshold,
			Weights: graphpkg.HybridWeights{
				VectorWeight: vectorWeight,
				GraphWeight:  graphWeight,
				EdgeWeight:   edgeWeight,
			},
		}
		
		if maxDepth > 0 {
			query.GraphFilter = &graphpkg.GraphFilter{
				MaxDepth: maxDepth,
			}
		}
		
		ctx := context.Background()
		results, err := graph.HybridSearch(ctx, query)
		if err != nil {
			return fmt.Errorf("hybrid search failed: %w", err)
		}
		
		fmt.Printf("Found %d results:\n", len(results))
		for i, result := range results {
			fmt.Printf("%d. %s (combined: %.4f, vector: %.4f, graph: %.4f, distance: %d)\n",
				i+1, result.Node.ID, result.CombinedScore, result.VectorScore, 
				result.GraphScore, result.Distance)
		}
		
		return nil
	},
}

var exportCmd = &cobra.Command{
	Use:   "export <format> <output-file>",
	Short: "Export graph to file",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		format := args[0]
		outputFile := args[1]
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		ctx := context.Background()
		
		file, err := os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer func() { _ = file.Close() }()
		
		if err := graph.Export(ctx, file, graphpkg.ExportFormat(format)); err != nil {
			return fmt.Errorf("export failed: %w", err)
		}
		
		fmt.Printf("Graph exported to %s in %s format\n", outputFile, format)
		return nil
	},
}

var importCmd = &cobra.Command{
	Use:   "import <format> <input-file>",
	Short: "Import graph from file",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		format := args[0]
		inputFile := args[1]
		
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		ctx := context.Background()
		
		file, err := os.Open(inputFile)
		if err != nil {
			return fmt.Errorf("failed to open input file: %w", err)
		}
		defer func() { _ = file.Close() }()
		
		if err := graph.Import(ctx, file, graphpkg.ExportFormat(format)); err != nil {
			return fmt.Errorf("import failed: %w", err)
		}
		
		fmt.Printf("Graph imported from %s\n", inputFile)
		return nil
	},
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Display graph statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, graph, err := openGraph()
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		
		ctx := context.Background()
		stats, err := graph.GetGraphStatistics(ctx)
		if err != nil {
			return fmt.Errorf("failed to get stats: %w", err)
		}
		
		outputJSON, _ := cmd.Flags().GetBool("json")
		if outputJSON {
			data, _ := json.MarshalIndent(stats, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Println("Graph Statistics:")
			fmt.Printf("  Node Count: %d\n", stats.NodeCount)
			fmt.Printf("  Edge Count: %d\n", stats.EdgeCount)
			fmt.Printf("  Average Degree: %.2f\n", stats.AverageDegree)
			fmt.Printf("  Density: %.4f\n", stats.Density)
			fmt.Printf("  Connected Components: %d\n", stats.ConnectedComponents)
		}
		
		return nil
	},
}

func openGraph() (*core.SQLiteStore, *graphpkg.GraphStore, error) {
	if dbPath == "" {
		return nil, nil, fmt.Errorf("database path not specified")
	}
	
	store, err := core.New(dbPath, dimensions)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open store: %w", err)
	}
	
	// Initialize the store if not already done
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		_ = store.Close()
		return nil, nil, fmt.Errorf("failed to initialize store: %w", err)
	}
	
	graph := graphpkg.NewGraphStore(store)
	// Ensure graph schema is initialized
	if err := graph.InitGraphSchema(ctx); err != nil {
		// Ignore error if schema already exists
		// This is expected when opening an existing database
		_ = err
	}
	
	return store, graph, nil
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVarP(&dbPath, "db", "d", "graph.db", "Database file path")
	rootCmd.PersistentFlags().IntVarP(&dimensions, "dimensions", "n", 0, "Vector dimensions (0 for auto)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	
	// Init command flags
	initCmd.Flags().Bool("enable-hnsw", false, "Enable HNSW index for fast vector search")
	
	// Node commands
	nodeCmd.AddCommand(nodeAddCmd, nodeGetCmd, nodeListCmd, nodeDeleteCmd)
	nodeAddCmd.Flags().String("content", "", "Node content")
	nodeAddCmd.Flags().String("type", "", "Node type")
	nodeAddCmd.Flags().String("vector", "", "Vector values (comma-separated)")
	nodeAddCmd.Flags().String("properties", "", "Properties as JSON")
	
	nodeGetCmd.Flags().Bool("json", false, "Output as JSON")
	
	nodeListCmd.Flags().String("type", "", "Filter by node type")
	nodeListCmd.Flags().Int("limit", 0, "Limit number of results")
	nodeListCmd.Flags().Bool("json", false, "Output as JSON")
	
	// Edge commands
	edgeCmd.AddCommand(edgeAddCmd, edgeListCmd)
	edgeAddCmd.Flags().String("type", "", "Edge type")
	edgeAddCmd.Flags().Float64("weight", 1.0, "Edge weight")
	edgeAddCmd.Flags().String("properties", "", "Properties as JSON")
	
	edgeListCmd.Flags().String("direction", "both", "Direction (in/out/both)")
	edgeListCmd.Flags().Bool("json", false, "Output as JSON")
	
	// Search commands
	searchCmd.AddCommand(searchVectorCmd, searchHybridCmd)
	searchVectorCmd.Flags().String("vector", "", "Query vector (comma-separated)")
	searchVectorCmd.Flags().Int("top-k", 10, "Number of results")
	searchVectorCmd.Flags().Float64("threshold", 0.0, "Similarity threshold")
	searchVectorCmd.Flags().Bool("hnsw", false, "Use HNSW index")
	_ = searchVectorCmd.MarkFlagRequired("vector")
	
	searchHybridCmd.Flags().String("vector", "", "Query vector (comma-separated)")
	searchHybridCmd.Flags().String("start-node", "", "Start node for graph search")
	searchHybridCmd.Flags().Int("top-k", 10, "Number of results")
	searchHybridCmd.Flags().Float64("threshold", 0.0, "Similarity threshold")
	searchHybridCmd.Flags().Float64("vector-weight", 0.5, "Vector similarity weight")
	searchHybridCmd.Flags().Float64("graph-weight", 0.3, "Graph proximity weight")
	searchHybridCmd.Flags().Float64("edge-weight", 0.2, "Edge strength weight")
	searchHybridCmd.Flags().Int("max-depth", 3, "Maximum graph traversal depth")
	
	// Stats command
	statsCmd.Flags().Bool("json", false, "Output as JSON")
	
	// Add all commands to root
	rootCmd.AddCommand(initCmd, nodeCmd, edgeCmd, searchCmd, exportCmd, importCmd, statsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}