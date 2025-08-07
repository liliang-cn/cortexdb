package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/liliang-cn/sqvect"
)

func main() {
	fmt.Println("sqvect Absolute Path Example")
	fmt.Println("============================")

	// Example 1: Relative path (current directory)
	fmt.Println("\n1. Using relative path:")
	relativeStore, err := sqvect.New("relative_example.db", 3)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := relativeStore.Close(); err != nil {
			log.Printf("Warning: failed to close relative store: %v", err)
		}
	}()
	defer func() {
		if err := os.Remove("relative_example.db"); err != nil {
			log.Printf("Warning: failed to remove relative_example.db: %v", err)
		}
	}()

	ctx := context.Background()
	if err := relativeStore.Init(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println("   ✓ Created store with relative path: relative_example.db")

	// Example 2: Absolute path in home directory
	fmt.Println("\n2. Using absolute path in home directory:")
	homeDir, _ := os.UserHomeDir()
	absolutePath := filepath.Join(homeDir, "sqvect_absolute_example.db")
	
	absoluteStore, err := sqvect.New(absolutePath, 3)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := absoluteStore.Close(); err != nil {
			log.Printf("Warning: failed to close absolute store: %v", err)
		}
	}()
	defer func() {
		if err := os.Remove(absolutePath); err != nil {
			log.Printf("Warning: failed to remove %s: %v", absolutePath, err)
		}
	}()

	if err := absoluteStore.Init(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   ✓ Created store with absolute path: %s\n", absolutePath)

	// Example 3: Absolute path in /tmp directory
	fmt.Println("\n3. Using absolute path in /tmp:")
	tmpPath := "/tmp/sqvect_temp_example.db"
	
	tmpStore, err := sqvect.New(tmpPath, 3)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := tmpStore.Close(); err != nil {
			log.Printf("Warning: failed to close tmp store: %v", err)
		}
	}()
	defer func() {
		if err := os.Remove(tmpPath); err != nil {
			log.Printf("Warning: failed to remove %s: %v", tmpPath, err)
		}
	}()

	if err := tmpStore.Init(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   ✓ Created store with absolute path: %s\n", tmpPath)

	// Example 4: Create directory if it doesn't exist
	fmt.Println("\n4. Creating directory and using absolute path:")
	nestedDir := filepath.Join(os.TempDir(), "sqvect_test", "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(filepath.Join(os.TempDir(), "sqvect_test")); err != nil {
			log.Printf("Warning: failed to remove test directory: %v", err)
		}
	}()

	nestedPath := filepath.Join(nestedDir, "nested_example.db")
	nestedStore, err := sqvect.New(nestedPath, 3)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := nestedStore.Close(); err != nil {
			log.Printf("Warning: failed to close nested store: %v", err)
		}
	}()

	if err := nestedStore.Init(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("   ✓ Created store in nested directory: %s\n", nestedPath)

	// Test that all stores work by adding data
	fmt.Println("\n5. Testing functionality with absolute paths:")
	
	testStores := []*sqvect.SQLiteStore{relativeStore, absoluteStore, tmpStore, nestedStore}
	storeNames := []string{"relative", "absolute (home)", "absolute (tmp)", "nested"}
	
	for i, store := range testStores {
		embedding := sqvect.Embedding{
			ID:      fmt.Sprintf("test_%d", i),
			Vector:  []float32{1.0, float32(i), 0.0},
			Content: fmt.Sprintf("Test content for %s store", storeNames[i]),
		}
		
		if err := store.Upsert(ctx, &embedding); err != nil {
			log.Fatalf("Failed to upsert to %s store: %v", storeNames[i], err)
		}
		
		// Search to verify it works
		results, err := store.Search(ctx, []float32{1.0, float32(i), 0.0}, sqvect.SearchOptions{TopK: 1})
		if err != nil {
			log.Fatalf("Failed to search %s store: %v", storeNames[i], err)
		}
		
		if len(results) == 1 && results[0].ID == fmt.Sprintf("test_%d", i) {
			fmt.Printf("   ✓ %s store: Successfully stored and retrieved data\n", storeNames[i])
		} else {
			fmt.Printf("   ✗ %s store: Data retrieval failed\n", storeNames[i])
		}
	}

	fmt.Println("\n6. Path validation examples:")
	
	// Example of various path formats that work
	validPaths := []string{
		"simple.db",                           // Relative path
		"./relative/path.db",                  // Relative with ./
		"/absolute/path/database.db",          // Unix absolute
		filepath.Join(os.TempDir(), "test.db"), // Cross-platform absolute
	}
	
	fmt.Println("   Valid path formats:")
	for _, path := range validPaths {
		fmt.Printf("     ✓ %s\n", path)
	}
	
	// Example of invalid paths (that would fail at Init time, not New time)
	fmt.Println("   Paths that would fail at Init() if parent directory doesn't exist:")
	fmt.Println("     ⚠  /nonexistent/directory/database.db")
	fmt.Println("     ⚠  /root/protected/database.db (permission denied)")

	fmt.Println("\n✅ All absolute path examples completed successfully!")
	fmt.Println("\nKey points:")
	fmt.Println("- sqvect accepts any valid file path (relative or absolute)")
	fmt.Println("- The parent directory must exist (sqvect won't create directories)")
	fmt.Println("- File permissions must allow read/write access")
	fmt.Println("- The .db extension is recommended but not required")
}