package graph

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// GraphML export/import structures

// GraphMLDocument represents a GraphML document
type GraphMLDocument struct {
	XMLName xml.Name       `xml:"graphml"`
	XMLNS   string         `xml:"xmlns,attr"`
	Keys    []GraphMLKey   `xml:"key"`
	Graph   GraphMLGraph   `xml:"graph"`
}

// GraphMLKey represents a GraphML key definition
type GraphMLKey struct {
	ID       string `xml:"id,attr"`
	For      string `xml:"for,attr"`
	AttrName string `xml:"attr.name,attr"`
	AttrType string `xml:"attr.type,attr"`
}

// GraphMLGraph represents a GraphML graph
type GraphMLGraph struct {
	ID            string         `xml:"id,attr"`
	EdgeDefault   string         `xml:"edgedefault,attr"`
	Nodes         []GraphMLNode  `xml:"node"`
	Edges         []GraphMLEdge  `xml:"edge"`
}

// GraphMLNode represents a GraphML node
type GraphMLNode struct {
	ID   string         `xml:"id,attr"`
	Data []GraphMLData  `xml:"data"`
}

// GraphMLEdge represents a GraphML edge
type GraphMLEdge struct {
	ID     string         `xml:"id,attr"`
	Source string         `xml:"source,attr"`
	Target string         `xml:"target,attr"`
	Data   []GraphMLData  `xml:"data"`
}

// GraphMLData represents GraphML data
type GraphMLData struct {
	Key   string `xml:"key,attr"`
	Value string `xml:",chardata"`
}

// ExportGraphML exports the graph to GraphML format
func (g *GraphStore) ExportGraphML(ctx context.Context, writer io.Writer) error {
	// Get all nodes
	nodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %w", err)
	}
	
	// Get all edges
	allEdges := make([]*GraphEdge, 0)
	for _, node := range nodes {
		edges, err := g.GetEdges(ctx, node.ID, "out")
		if err != nil {
			continue
		}
		allEdges = append(allEdges, edges...)
	}
	
	// Create GraphML document
	doc := GraphMLDocument{
		XMLNS: "http://graphml.graphdrawing.org/xmlns",
		Keys: []GraphMLKey{
			{ID: "d0", For: "node", AttrName: "content", AttrType: "string"},
			{ID: "d1", For: "node", AttrName: "type", AttrType: "string"},
			{ID: "d2", For: "node", AttrName: "vector", AttrType: "string"},
			{ID: "d3", For: "node", AttrName: "properties", AttrType: "string"},
			{ID: "d4", For: "edge", AttrName: "type", AttrType: "string"},
			{ID: "d5", For: "edge", AttrName: "weight", AttrType: "double"},
			{ID: "d6", For: "edge", AttrName: "properties", AttrType: "string"},
		},
		Graph: GraphMLGraph{
			ID:          "G",
			EdgeDefault: "directed",
			Nodes:       make([]GraphMLNode, 0, len(nodes)),
			Edges:       make([]GraphMLEdge, 0, len(allEdges)),
		},
	}
	
	// Add nodes
	for _, node := range nodes {
		gmlNode := GraphMLNode{
			ID: node.ID,
			Data: []GraphMLData{
				{Key: "d0", Value: node.Content},
				{Key: "d1", Value: node.NodeType},
			},
		}
		
		// Encode vector as JSON
		if len(node.Vector) > 0 {
			vectorJSON, _ := json.Marshal(node.Vector)
			gmlNode.Data = append(gmlNode.Data, GraphMLData{Key: "d2", Value: string(vectorJSON)})
		}
		
		// Encode properties as JSON
		if node.Properties != nil {
			propsJSON, _ := json.Marshal(node.Properties)
			gmlNode.Data = append(gmlNode.Data, GraphMLData{Key: "d3", Value: string(propsJSON)})
		}
		
		doc.Graph.Nodes = append(doc.Graph.Nodes, gmlNode)
	}
	
	// Add edges
	edgeSet := make(map[string]bool) // To avoid duplicates
	for _, edge := range allEdges {
		if edgeSet[edge.ID] {
			continue
		}
		edgeSet[edge.ID] = true
		
		gmlEdge := GraphMLEdge{
			ID:     edge.ID,
			Source: edge.FromNodeID,
			Target: edge.ToNodeID,
			Data: []GraphMLData{
				{Key: "d4", Value: edge.EdgeType},
				{Key: "d5", Value: fmt.Sprintf("%f", edge.Weight)},
			},
		}
		
		// Encode properties as JSON
		if edge.Properties != nil {
			propsJSON, _ := json.Marshal(edge.Properties)
			gmlEdge.Data = append(gmlEdge.Data, GraphMLData{Key: "d6", Value: string(propsJSON)})
		}
		
		doc.Graph.Edges = append(doc.Graph.Edges, gmlEdge)
	}
	
	// Write XML
	encoder := xml.NewEncoder(writer)
	encoder.Indent("", "  ")
	
	// Write XML header
	writer.Write([]byte(xml.Header))
	
	// Encode document
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("failed to encode GraphML: %w", err)
	}
	
	return nil
}

// ImportGraphML imports a graph from GraphML format
func (g *GraphStore) ImportGraphML(ctx context.Context, reader io.Reader) error {
	decoder := xml.NewDecoder(reader)
	
	var doc GraphMLDocument
	if err := decoder.Decode(&doc); err != nil {
		return fmt.Errorf("failed to decode GraphML: %w", err)
	}
	
	// Create key mapping
	keyMap := make(map[string]string)
	for _, key := range doc.Keys {
		keyMap[key.ID] = key.AttrName
	}
	
	// Import nodes
	nodes := make([]*GraphNode, 0, len(doc.Graph.Nodes))
	for _, gmlNode := range doc.Graph.Nodes {
		node := &GraphNode{
			ID:         gmlNode.ID,
			Properties: make(map[string]interface{}),
		}
		
		// Parse data fields
		for _, data := range gmlNode.Data {
			attrName := keyMap[data.Key]
			switch attrName {
			case "content":
				node.Content = data.Value
			case "type":
				node.NodeType = data.Value
			case "vector":
				if err := json.Unmarshal([]byte(data.Value), &node.Vector); err != nil {
					// If vector parsing fails, create a default one
					node.Vector = make([]float32, 3)
				}
			case "properties":
				json.Unmarshal([]byte(data.Value), &node.Properties)
			}
		}
		
		// Ensure node has a vector
		if len(node.Vector) == 0 {
			node.Vector = make([]float32, 3) // Default vector
		}
		
		nodes = append(nodes, node)
	}
	
	// Batch import nodes
	if _, err := g.UpsertNodesBatch(ctx, nodes); err != nil {
		return fmt.Errorf("failed to import nodes: %w", err)
	}
	
	// Import edges
	edges := make([]*GraphEdge, 0, len(doc.Graph.Edges))
	for _, gmlEdge := range doc.Graph.Edges {
		edge := &GraphEdge{
			ID:         gmlEdge.ID,
			FromNodeID: gmlEdge.Source,
			ToNodeID:   gmlEdge.Target,
			Weight:     1.0,
			Properties: make(map[string]interface{}),
		}
		
		// Parse data fields
		for _, data := range gmlEdge.Data {
			attrName := keyMap[data.Key]
			switch attrName {
			case "type":
				edge.EdgeType = data.Value
			case "weight":
				if w, err := strconv.ParseFloat(data.Value, 64); err == nil {
					edge.Weight = w
				}
			case "properties":
				json.Unmarshal([]byte(data.Value), &edge.Properties)
			}
		}
		
		edges = append(edges, edge)
	}
	
	// Batch import edges
	if _, err := g.UpsertEdgesBatch(ctx, edges); err != nil {
		return fmt.Errorf("failed to import edges: %w", err)
	}
	
	return nil
}

// GEXF export/import structures

// GEXFDocument represents a GEXF document
type GEXFDocument struct {
	XMLName xml.Name  `xml:"gexf"`
	XMLNS   string    `xml:"xmlns,attr"`
	Version string    `xml:"version,attr"`
	Meta    GEXFMeta  `xml:"meta"`
	Graph   GEXFGraph `xml:"graph"`
}

// GEXFMeta represents GEXF metadata
type GEXFMeta struct {
	Creator     string `xml:"creator"`
	Description string `xml:"description"`
}

// GEXFGraph represents a GEXF graph
type GEXFGraph struct {
	Mode       string       `xml:"mode,attr"`
	DefaultEdgeType string  `xml:"defaultedgetype,attr"`
	Attributes GEXFAttrs    `xml:"attributes"`
	Nodes      GEXFNodes    `xml:"nodes"`
	Edges      GEXFEdges    `xml:"edges"`
}

// GEXFAttrs represents GEXF attributes
type GEXFAttrs struct {
	Class string     `xml:"class,attr"`
	Attrs []GEXFAttr `xml:"attribute"`
}

// GEXFAttr represents a GEXF attribute
type GEXFAttr struct {
	ID    string `xml:"id,attr"`
	Title string `xml:"title,attr"`
	Type  string `xml:"type,attr"`
}

// GEXFNodes represents GEXF nodes container
type GEXFNodes struct {
	Nodes []GEXFNode `xml:"node"`
}

// GEXFNode represents a GEXF node
type GEXFNode struct {
	ID    string       `xml:"id,attr"`
	Label string       `xml:"label,attr"`
	AttValues []GEXFAttValue `xml:"attvalues>attvalue"`
}

// GEXFEdges represents GEXF edges container
type GEXFEdges struct {
	Edges []GEXFEdge `xml:"edge"`
}

// GEXFEdge represents a GEXF edge
type GEXFEdge struct {
	ID     string  `xml:"id,attr"`
	Source string  `xml:"source,attr"`
	Target string  `xml:"target,attr"`
	Weight float64 `xml:"weight,attr,omitempty"`
	Type   string  `xml:"type,attr,omitempty"`
}

// GEXFAttValue represents a GEXF attribute value
type GEXFAttValue struct {
	For   string `xml:"for,attr"`
	Value string `xml:"value,attr"`
}

// ExportGEXF exports the graph to GEXF format
func (g *GraphStore) ExportGEXF(ctx context.Context, writer io.Writer) error {
	// Get all nodes
	nodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %w", err)
	}
	
	// Get all edges
	allEdges := make([]*GraphEdge, 0)
	for _, node := range nodes {
		edges, err := g.GetEdges(ctx, node.ID, "out")
		if err != nil {
			continue
		}
		allEdges = append(allEdges, edges...)
	}
	
	// Create GEXF document
	doc := GEXFDocument{
		XMLNS:   "http://www.gexf.net/1.3",
		Version: "1.3",
		Meta: GEXFMeta{
			Creator:     "sqvect",
			Description: "Graph exported from sqvect",
		},
		Graph: GEXFGraph{
			Mode:            "static",
			DefaultEdgeType: "directed",
			Attributes: GEXFAttrs{
				Class: "node",
				Attrs: []GEXFAttr{
					{ID: "0", Title: "content", Type: "string"},
					{ID: "1", Title: "type", Type: "string"},
					{ID: "2", Title: "vector", Type: "string"},
					{ID: "3", Title: "properties", Type: "string"},
				},
			},
			Nodes: GEXFNodes{
				Nodes: make([]GEXFNode, 0, len(nodes)),
			},
			Edges: GEXFEdges{
				Edges: make([]GEXFEdge, 0, len(allEdges)),
			},
		},
	}
	
	// Add nodes
	for _, node := range nodes {
		gexfNode := GEXFNode{
			ID:    node.ID,
			Label: node.Content,
			AttValues: []GEXFAttValue{
				{For: "0", Value: node.Content},
				{For: "1", Value: node.NodeType},
			},
		}
		
		// Encode vector as JSON
		if len(node.Vector) > 0 {
			vectorJSON, _ := json.Marshal(node.Vector)
			gexfNode.AttValues = append(gexfNode.AttValues, GEXFAttValue{For: "2", Value: string(vectorJSON)})
		}
		
		// Encode properties as JSON
		if node.Properties != nil {
			propsJSON, _ := json.Marshal(node.Properties)
			gexfNode.AttValues = append(gexfNode.AttValues, GEXFAttValue{For: "3", Value: string(propsJSON)})
		}
		
		doc.Graph.Nodes.Nodes = append(doc.Graph.Nodes.Nodes, gexfNode)
	}
	
	// Add edges
	edgeSet := make(map[string]bool)
	for _, edge := range allEdges {
		if edgeSet[edge.ID] {
			continue
		}
		edgeSet[edge.ID] = true
		
		gexfEdge := GEXFEdge{
			ID:     edge.ID,
			Source: edge.FromNodeID,
			Target: edge.ToNodeID,
			Weight: edge.Weight,
			Type:   edge.EdgeType,
		}
		
		doc.Graph.Edges.Edges = append(doc.Graph.Edges.Edges, gexfEdge)
	}
	
	// Write XML
	encoder := xml.NewEncoder(writer)
	encoder.Indent("", "  ")
	
	// Write XML header
	writer.Write([]byte(xml.Header))
	
	// Encode document
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("failed to encode GEXF: %w", err)
	}
	
	return nil
}

// ImportGEXF imports a graph from GEXF format
func (g *GraphStore) ImportGEXF(ctx context.Context, reader io.Reader) error {
	decoder := xml.NewDecoder(reader)
	
	var doc GEXFDocument
	if err := decoder.Decode(&doc); err != nil {
		return fmt.Errorf("failed to decode GEXF: %w", err)
	}
	
	// Create attribute mapping
	attrMap := make(map[string]string)
	for _, attr := range doc.Graph.Attributes.Attrs {
		attrMap[attr.ID] = attr.Title
	}
	
	// Import nodes
	nodes := make([]*GraphNode, 0, len(doc.Graph.Nodes.Nodes))
	for _, gexfNode := range doc.Graph.Nodes.Nodes {
		node := &GraphNode{
			ID:         gexfNode.ID,
			Content:    gexfNode.Label,
			Properties: make(map[string]interface{}),
		}
		
		// Parse attribute values
		for _, attValue := range gexfNode.AttValues {
			attrName := attrMap[attValue.For]
			switch attrName {
			case "content":
				node.Content = attValue.Value
			case "type":
				node.NodeType = attValue.Value
			case "vector":
				if err := json.Unmarshal([]byte(attValue.Value), &node.Vector); err != nil {
					node.Vector = make([]float32, 3)
				}
			case "properties":
				json.Unmarshal([]byte(attValue.Value), &node.Properties)
			}
		}
		
		// Ensure node has a vector
		if len(node.Vector) == 0 {
			node.Vector = make([]float32, 3)
		}
		
		nodes = append(nodes, node)
	}
	
	// Batch import nodes
	if _, err := g.UpsertNodesBatch(ctx, nodes); err != nil {
		return fmt.Errorf("failed to import nodes: %w", err)
	}
	
	// Import edges
	edges := make([]*GraphEdge, 0, len(doc.Graph.Edges.Edges))
	for _, gexfEdge := range doc.Graph.Edges.Edges {
		edge := &GraphEdge{
			ID:         gexfEdge.ID,
			FromNodeID: gexfEdge.Source,
			ToNodeID:   gexfEdge.Target,
			EdgeType:   gexfEdge.Type,
			Weight:     gexfEdge.Weight,
			Properties: make(map[string]interface{}),
		}
		
		if edge.Weight == 0 {
			edge.Weight = 1.0
		}
		
		edges = append(edges, edge)
	}
	
	// Batch import edges
	if _, err := g.UpsertEdgesBatch(ctx, edges); err != nil {
		return fmt.Errorf("failed to import edges: %w", err)
	}
	
	return nil
}

// ExportJSON exports the graph to JSON format
func (g *GraphStore) ExportJSON(ctx context.Context, writer io.Writer) error {
	// Get all nodes
	nodes, err := g.GetAllNodes(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %w", err)
	}
	
	// Get all edges
	allEdges := make([]*GraphEdge, 0)
	edgeSet := make(map[string]bool)
	
	for _, node := range nodes {
		edges, err := g.GetEdges(ctx, node.ID, "out")
		if err != nil {
			continue
		}
		for _, edge := range edges {
			if !edgeSet[edge.ID] {
				allEdges = append(allEdges, edge)
				edgeSet[edge.ID] = true
			}
		}
	}
	
	// Create JSON structure
	graphData := map[string]interface{}{
		"nodes": nodes,
		"edges": allEdges,
		"metadata": map[string]interface{}{
			"node_count": len(nodes),
			"edge_count": len(allEdges),
			"format":     "sqvect-graph-v1",
		},
	}
	
	// Encode to JSON
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	
	if err := encoder.Encode(graphData); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	
	return nil
}

// ImportJSON imports a graph from JSON format
func (g *GraphStore) ImportJSON(ctx context.Context, reader io.Reader) error {
	var graphData struct {
		Nodes []*GraphNode `json:"nodes"`
		Edges []*GraphEdge `json:"edges"`
	}
	
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&graphData); err != nil {
		return fmt.Errorf("failed to decode JSON: %w", err)
	}
	
	// Batch import nodes
	if len(graphData.Nodes) > 0 {
		if _, err := g.UpsertNodesBatch(ctx, graphData.Nodes); err != nil {
			return fmt.Errorf("failed to import nodes: %w", err)
		}
	}
	
	// Batch import edges
	if len(graphData.Edges) > 0 {
		if _, err := g.UpsertEdgesBatch(ctx, graphData.Edges); err != nil {
			return fmt.Errorf("failed to import edges: %w", err)
		}
	}
	
	return nil
}

// ExportFormat represents supported export formats
type ExportFormat string

const (
	FormatGraphML ExportFormat = "graphml"
	FormatGEXF    ExportFormat = "gexf"
	FormatJSON    ExportFormat = "json"
)

// Export exports the graph in the specified format
func (g *GraphStore) Export(ctx context.Context, writer io.Writer, format ExportFormat) error {
	switch format {
	case FormatGraphML:
		return g.ExportGraphML(ctx, writer)
	case FormatGEXF:
		return g.ExportGEXF(ctx, writer)
	case FormatJSON:
		return g.ExportJSON(ctx, writer)
	default:
		return fmt.Errorf("unsupported export format: %s", format)
	}
}

// Import imports a graph in the specified format
func (g *GraphStore) Import(ctx context.Context, reader io.Reader, format ExportFormat) error {
	switch format {
	case FormatGraphML:
		return g.ImportGraphML(ctx, reader)
	case FormatGEXF:
		return g.ImportGEXF(ctx, reader)
	case FormatJSON:
		return g.ImportJSON(ctx, reader)
	default:
		return fmt.Errorf("unsupported import format: %s", format)
	}
}

// DetectFormat attempts to detect the format of the input
func DetectFormat(reader io.Reader) (ExportFormat, error) {
	// Read first few bytes to detect format
	buf := make([]byte, 512)
	n, err := reader.Read(buf)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("failed to read: %w", err)
	}
	
	content := string(buf[:n])
	content = strings.TrimSpace(content)
	
	// Check for XML formats
	if strings.HasPrefix(content, "<?xml") || strings.HasPrefix(content, "<graphml") {
		if strings.Contains(content, "graphml") {
			return FormatGraphML, nil
		}
		if strings.Contains(content, "gexf") {
			return FormatGEXF, nil
		}
	}
	
	// Check for JSON
	if strings.HasPrefix(content, "{") {
		return FormatJSON, nil
	}
	
	return "", fmt.Errorf("unable to detect format")
}