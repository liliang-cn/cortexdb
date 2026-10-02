package index

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/liliang-cn/cortexdb/v2/pkg/quantization"
)

// clusteredVectors draws n unit vectors around `clusters` centers that share
// a common per-dimension offset — the shape of real embeddings, which are
// neither uniform on the sphere nor centered on zero. Uniform random vectors
// would flatter any quantizer: their neighbours are all nearly equidistant,
// so recall says little, and they are already zero-centered, so centering
// could not matter.
//
// offset: std of the shared per-dimension mean; spread: std of cluster
// centers around it; noise: std of a vector around its center.
func clusteredVectors(n, dim, clusters int, offset, spread, noise float64, seed uint64) [][]float32 {
	rng := rand.New(rand.NewPCG(seed, 0xb1a7))
	shared := make([]float64, dim)
	for d := range shared {
		shared[d] = rng.NormFloat64() * offset
	}
	centers := make([][]float64, clusters)
	for c := range centers {
		centers[c] = make([]float64, dim)
		for d := range centers[c] {
			centers[c][d] = shared[d] + rng.NormFloat64()*spread
		}
	}
	out := make([][]float32, n)
	for i := range out {
		c := centers[rng.IntN(clusters)]
		v := make([]float32, dim)
		norm := 0.0
		for d := range v {
			x := c[d] + rng.NormFloat64()*noise
			v[d] = float32(x)
			norm += x * x
		}
		inv := float32(1 / math.Sqrt(norm))
		for d := range v {
			v[d] *= inv
		}
		out[i] = v
	}
	return out
}

// embeddingLikeVectors draws n clustered unit vectors with the second-order
// shape measured on real 768-d text embeddings (embeddinggemma over the
// brain snapshot and over Go doc comments): variance concentrated in a few
// dozen directions — a participation ratio of roughly 60–100 rather than the
// ~700 of isotropic noise — and per-dimension means around 0.4 standard
// deviations off zero.
//
// Structure lives in a latent space of `latent` dimensions with a power-law
// spectrum (direction j has standard deviation j^-0.5), where cluster centers
// and the points around them are drawn; a fixed random linear map lifts it to
// dim, plus a little isotropic noise and a shared offset. clusteredVectors'
// isotropic within-cluster noise makes every member of a cluster nearly
// equidistant from every other — a stress case no text embedding has.
func embeddingLikeVectors(n, dim, clusters, latent int, seed uint64) [][]float32 {
	rng := rand.New(rand.NewPCG(seed, 0xe3b))
	scale := make([]float64, latent)
	for j := range scale {
		scale[j] = math.Pow(float64(j+1), -0.35)
	}
	lift := make([][]float64, dim)
	for d := range lift {
		lift[d] = make([]float64, latent)
		for j := range lift[d] {
			lift[d][j] = rng.NormFloat64() / math.Sqrt(float64(latent))
		}
	}
	centers := make([][]float64, clusters)
	for c := range centers {
		centers[c] = make([]float64, latent)
		for j := range centers[c] {
			centers[c][j] = rng.NormFloat64() * scale[j]
		}
	}
	offset := make([]float64, dim)
	for d := range offset {
		offset[d] = rng.NormFloat64() * 0.2
	}
	out := make([][]float32, n)
	z := make([]float64, latent)
	for i := range out {
		c := centers[rng.IntN(clusters)]
		for j := range z {
			z[j] = c[j] + rng.NormFloat64()*scale[j]*0.6
		}
		v := make([]float32, dim)
		norm := 0.0
		for d := range v {
			x := offset[d] + rng.NormFloat64()*0.16
			for j, w := range lift[d] {
				x += w * z[j]
			}
			v[d] = float32(x)
			norm += x * x
		}
		inv := float32(1 / math.Sqrt(norm))
		for d := range v {
			v[d] *= inv
		}
		out[i] = v
	}
	return out
}

// exactTopK is brute-force cosine top-k, the ground truth recall is measured
// against.
func exactTopK(vectors [][]float32, ids []string, query []float32, k int) []string {
	type r struct {
		id string
		d  float32
	}
	all := make([]r, len(vectors))
	for i, v := range vectors {
		all[i] = r{ids[i], CosineDistance(query, v)}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].d < all[b].d })
	out := make([]string, 0, k)
	for _, x := range all[:k] {
		out = append(out, x.id)
	}
	return out
}

func recallAt(got, want []string) float64 {
	set := map[string]bool{}
	for _, id := range want {
		set[id] = true
	}
	hit := 0
	for _, id := range got {
		if set[id] {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}

func buildBinary(t testing.TB, vectors [][]float32, opts BinaryIndexOptions) (*BinaryIndex, []string) {
	ids := make([]string, len(vectors))
	for i := range ids {
		ids[i] = fmt.Sprintf("v%06d", i)
	}
	idx := NewBinaryIndex(len(vectors[0]), opts)
	if err := idx.Rebuild(ids, vectors); err != nil {
		t.Fatal(err)
	}
	return idx, ids
}

func meanRecall(idx *BinaryIndex, vectors [][]float32, ids []string, queries [][]float32, k, oversample int) float64 {
	total := 0.0
	for _, q := range queries {
		got, _ := idx.SearchOversampled(q, k, oversample)
		total += recallAt(got, exactTopK(vectors, ids, q, k))
	}
	return total / float64(len(queries))
}

func TestBinarySearchWithRescoringFindsTheExactTopTen(t *testing.T) {
	all := embeddingLikeVectors(5050, 384, 100, 64, 1)
	vectors, queries := all[:5000], all[5000:]
	idx, ids := buildBinary(t, vectors, BinaryIndexOptions{Center: true, KeepVectors: true})
	recall := meanRecall(idx, vectors, ids, queries, 10, DefaultBinaryOversample)
	if recall < 0.95 {
		t.Fatalf("recall@10 at the default oversample %d is %.3f, want >= 0.95", DefaultBinaryOversample, recall)
	}
	// The same codes ranked by Hamming distance alone, no rescoring: the
	// shortlist is right but its order is not, which is why the rescoring
	// step exists.
	codesOnly, _ := buildBinary(t, vectors, BinaryIndexOptions{Center: true})
	if r := meanRecall(codesOnly, vectors, ids, queries, 10, DefaultBinaryOversample); r >= recall {
		t.Fatalf("Hamming order alone reached recall %.3f, rescoring %.3f — the fixture cannot tell them apart", r, recall)
	}
}

func TestCenteringRecoversRecallOnEmbeddingsThatAreNotZeroCentered(t *testing.T) {
	// A strong shared offset: most dimensions have the same sign in every
	// vector, so their sign bits carry nothing until the mean is removed.
	all := clusteredVectors(5050, 256, 50, 2.0, 1.0, 0.5, 2)
	vectors, queries := all[:5000], all[5000:]
	centered, ids := buildBinary(t, vectors, BinaryIndexOptions{Center: true, KeepVectors: true})
	plain, _ := buildBinary(t, vectors, BinaryIndexOptions{KeepVectors: true})
	rc := meanRecall(centered, vectors, ids, queries, 10, 2)
	rp := meanRecall(plain, vectors, ids, queries, 10, 2)
	if rc <= rp {
		t.Fatalf("centered recall %.3f is not above uncentered %.3f on offset data", rc, rp)
	}
}

func TestHammingDistanceCountsEveryDifferingBit(t *testing.T) {
	cases := []struct {
		a, b []uint64
		want int
	}{
		{[]uint64{0}, []uint64{0}, 0},
		{[]uint64{1}, []uint64{0}, 1},
		{[]uint64{1 << 63}, []uint64{0}, 1},                     // the top bit of a word
		{[]uint64{0xFFFFFFFF00000000}, []uint64{0}, 32},         // the high half
		{[]uint64{math.MaxUint64}, []uint64{0}, 64},             // a whole word
		{[]uint64{math.MaxUint64, 0, 5}, []uint64{0, 0, 6}, 66}, // across words
	}
	for _, c := range cases {
		if got := quantization.HammingWords(c.a, c.b); got != c.want {
			t.Errorf("HammingWords(%x, %x) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	// And against a bit-by-bit count over random codes.
	rng := rand.New(rand.NewPCG(3, 3))
	for i := 0; i < 200; i++ {
		a := []uint64{rng.Uint64(), rng.Uint64()}
		b := []uint64{rng.Uint64(), rng.Uint64()}
		want := 0
		for w := range a {
			for bit := 0; bit < 64; bit++ {
				if (a[w]>>bit)&1 != (b[w]>>bit)&1 {
					want++
				}
			}
		}
		if got := quantization.HammingWords(a, b); got != want {
			t.Fatalf("HammingWords = %d, bit count %d", got, want)
		}
	}
}

func TestCandidatesAreTheNearestCodesInHammingOrder(t *testing.T) {
	vectors := clusteredVectors(3000, 200, 30, 0.5, 1, 0.5, 4) // 200: a partial last word
	idx, ids := buildBinary(t, vectors, BinaryIndexOptions{})
	query := vectors[17]
	got, dists := idx.Candidates(query, 50)
	q := make([]uint64, quantization.BinaryWords(200))
	quantization.PackSigns(q, query, nil)
	type r struct {
		id string
		d  int
	}
	all := make([]r, len(vectors))
	for i, v := range vectors {
		c := make([]uint64, len(q))
		quantization.PackSigns(c, v, nil)
		all[i] = r{ids[i], quantization.HammingWords(q, c)}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].d < all[b].d })
	for i := range got {
		if got[i] != all[i].id || dists[i] != all[i].d {
			t.Fatalf("candidate %d: got %s@%d, want %s@%d", i, got[i], dists[i], all[i].id, all[i].d)
		}
	}
	if got[0] != ids[17] || dists[0] != 0 {
		t.Fatalf("a stored vector is not its own nearest code: %s@%d", got[0], dists[0])
	}
}

func TestBinaryCodesTakeOneThirtySecondOfTheFloats(t *testing.T) {
	vectors := clusteredVectors(1000, 768, 10, 1, 1, 0.5, 5)
	idx, _ := buildBinary(t, vectors, BinaryIndexOptions{Center: true})
	floatBytes := len(vectors) * 768 * 4
	if got := idx.CodeBytes(); got*32 != floatBytes {
		t.Fatalf("codes take %d bytes for %d bytes of float32, want 1/32", got, floatBytes)
	}
}

func TestBinaryIndexInsertReplacesAndDeleteRemoves(t *testing.T) {
	idx := NewBinaryIndex(4, BinaryIndexOptions{KeepVectors: true})
	_ = idx.Insert("a", []float32{1, 0, 0, 0})
	_ = idx.Insert("b", []float32{0, 1, 0, 0})
	_ = idx.Insert("c", []float32{0, 0, 1, 0})
	_ = idx.Insert("a", []float32{0, 0, 0, 1}) // replace, not add
	if idx.Size() != 3 {
		t.Fatalf("size %d after a replace, want 3", idx.Size())
	}
	if got, _ := idx.Search([]float32{0, 0, 0, 1}, 1); len(got) != 1 || got[0] != "a" {
		t.Fatalf("replaced vector not found: %v", got)
	}
	if !idx.Delete("a") || idx.Delete("a") {
		t.Fatal("Delete should report removing a present id once")
	}
	if got, _ := idx.Search([]float32{0, 0, 0, 1}, 3); len(got) != 2 {
		t.Fatalf("after delete: %v", got)
	}
	if got, _ := idx.Search([]float32{0, 0, 1, 0}, 1); got[0] != "c" {
		t.Fatalf("the moved vector lost its code: %v", got)
	}
	if err := idx.Insert("bad", []float32{1}); err == nil {
		t.Fatal("a wrong-dimension insert was accepted")
	}
}

func TestTheCenterIsRetrainedOnceTheDataHasDoubled(t *testing.T) {
	vectors := clusteredVectors(400, 64, 4, 1, 1, 0.5, 6)
	idx := NewBinaryIndex(64, BinaryIndexOptions{Center: true})
	for i, v := range vectors[:100] {
		_ = idx.Insert(fmt.Sprint(i), v)
	}
	if !idx.NeedsRetrain() {
		t.Fatal("a centering index with no center and 100 vectors should ask to be trained")
	}
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprint(i)
	}
	_ = idx.Rebuild(ids, vectors[:100])
	if idx.NeedsRetrain() {
		t.Fatal("just trained")
	}
	for i, v := range vectors[100:200] {
		_ = idx.Insert(fmt.Sprint(100+i), v)
	}
	if !idx.NeedsRetrain() {
		t.Fatal("twice the vectors the center was learned on, and no retrain asked")
	}
}

// binaryBenchData is built once: 100k embedding-like 768-d vectors and 100
// held-out queries.
var binaryBenchData struct {
	vectors, queries [][]float32
	ids              []string
}

func benchVectors(b *testing.B) ([][]float32, [][]float32, []string) {
	if binaryBenchData.vectors == nil {
		all := embeddingLikeVectors(100100, 768, 1000, 128, 7)
		binaryBenchData.vectors, binaryBenchData.queries = all[:100000], all[100000:]
		binaryBenchData.ids = make([]string, 100000)
		for i := range binaryBenchData.ids {
			binaryBenchData.ids[i] = fmt.Sprintf("v%06d", i)
		}
	}
	return binaryBenchData.vectors, binaryBenchData.queries, binaryBenchData.ids
}

func BenchmarkBinaryQuantizationSearch100k(b *testing.B) {
	vectors, queries, ids := benchVectors(b)
	idx := NewBinaryIndex(768, BinaryIndexOptions{Center: true, KeepVectors: true})
	if err := idx.Rebuild(ids, vectors); err != nil {
		b.Fatal(err)
	}
	for _, over := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("oversample=%d", over), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				idx.SearchOversampled(queries[i%len(queries)], 10, over)
			}
		})
	}
}

func BenchmarkBinaryQuantizationExactFlat100k(b *testing.B) {
	vectors, queries, ids := benchVectors(b)
	flat := NewFlatIndex(768, CosineDistance)
	for i, v := range vectors {
		_ = flat.Insert(ids[i], v)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		flat.Search(queries[i%len(queries)], 10)
	}
}

func BenchmarkBinaryQuantizationEncode768(b *testing.B) {
	vectors, _, _ := benchVectors(b)
	center := quantization.MeanVector(vectors[:1000], 768)
	dst := make([]uint64, quantization.BinaryWords(768))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		quantization.PackSigns(dst, vectors[i%len(vectors)], center)
	}
}
