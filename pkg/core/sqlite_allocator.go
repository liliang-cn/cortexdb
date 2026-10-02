package core

import (
	"sync"

	"modernc.org/libc"
	"modernc.org/libc/sys/types"
)

// pinAllocatorSlabs gives every small size class of the SQLite allocator a
// reserve of slabs that stay mapped for the life of the process.
//
// modernc.org/sqlite allocates through modernc.org/libc, whose allocator
// (modernc.org/memory) carves small blocks out of 64 KiB slabs and unmaps a
// slab the moment its last block is freed. A statement journal — which every
// write inside a transaction keeps once a table has triggers, as the change
// feed's do — is a run of 2 KiB chunks allocated during the statement and
// freed at its end. When no slab has room, the journal maps a fresh one and
// unmaps it again a statement later: 2.5 slab mappings per graph write, a
// third of its time with the feed on.
//
// The reserve: for each class, blocks are allocated until slabsPerClass
// distinct slabs hold one, the first block of each slab is kept forever, and
// the rest are freed. The kept block holds its slab's use count above zero,
// so the slab is never unmapped, and the freed blocks wait on the class's
// free list for the next statement's chunks.
func pinAllocatorSlabs() {
	pinSlabsOnce.Do(func() {
		tls := libc.NewTLS()
		defer tls.Close()
		for size := minSlabBlock; size <= maxSlabBlock; size <<= 1 {
			pinned := make(map[uintptr]bool, slabsPerClass)
			var spare []uintptr
			// A slab holds at most slabSize/size blocks, so this many
			// allocations always span slabsPerClass slabs.
			for i := 0; len(pinned) < slabsPerClass && i < (slabsPerClass+1)*slabSize/size; i++ {
				p := libc.Xmalloc(tls, types.Size_t(size))
				if p == 0 {
					break
				}
				if slab := p &^ (slabSize - 1); !pinned[slab] {
					pinned[slab] = true
					continue
				}
				spare = append(spare, p)
			}
			for _, p := range spare {
				libc.Xfree(tls, p)
			}
		}
	})
}

const (
	// slabSize is modernc.org/memory's page: 1<<16 on every platform.
	slabSize = 64 << 10
	// minSlabBlock and maxSlabBlock bound the slab-allocated classes; larger
	// blocks get a mapping of their own, which no reserve can help.
	minSlabBlock = 16
	maxSlabBlock = 16 << 10
	// slabsPerClass is how many slabs each class keeps mapped. Two leave a
	// class room for a statement's working set after long-lived allocations
	// have taken their share of the first.
	slabsPerClass = 4
)

var pinSlabsOnce sync.Once
