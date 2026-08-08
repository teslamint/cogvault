package index

import (
	"math"
	"testing"

	"github.com/teslamint/cogvault/internal/config"
)

func TestVecBlobRoundtrip(t *testing.T) {
	original := []float32{1.0, -2.5, 3.14, 0.0, -0.001}
	blob := vecToBlob(original)
	restored := blobToVec(blob)

	if len(restored) != len(original) {
		t.Fatalf("len = %d, want %d", len(restored), len(original))
	}
	for i := range original {
		if restored[i] != original[i] {
			t.Errorf("[%d] = %f, want %f", i, restored[i], original[i])
		}
	}
}

func TestDotProduct(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	if got := dotProduct(a, b); got != 0 {
		t.Errorf("orthogonal dot = %f, want 0", got)
	}

	c := []float32{0.6, 0.8, 0}
	d := []float32{0.6, 0.8, 0}
	if got := dotProduct(c, d); math.Abs(got-1.0) > 1e-6 {
		t.Errorf("identical unit dot = %f, want 1", got)
	}

	if got := dotProduct([]float32{1, 2}, []float32{1, 2, 3}); got != 0 {
		t.Errorf("mismatched len dot = %f, want 0", got)
	}
}

func TestStoreAndSearchEmbedding(t *testing.T) {
	idx := setupTestIndex(t)
	defer idx.Close()

	if err := idx.InitEmbeddingsTable(); err != nil {
		t.Fatalf("InitEmbeddingsTable: %v", err)
	}

	idx.Add("page-a.md", "Alpha content about Go programming", map[string]string{"title": "Alpha", "type": "source", "category": "", "tags": ""})
	idx.Add("page-b.md", "Beta content about Go concurrency", map[string]string{"title": "Beta", "type": "source", "category": "", "tags": ""})
	idx.Add("page-c.md", "Gamma content about cooking recipes", map[string]string{"title": "Gamma", "type": "source", "category": "", "tags": ""})

	vecA := []float32{0.9, 0.1, 0.0}
	normalize(vecA)
	vecB := []float32{0.85, 0.15, 0.05}
	normalize(vecB)
	vecC := []float32{0.1, 0.1, 0.95}
	normalize(vecC)

	model := "test-embed"
	if err := idx.StoreEmbedding("page-a.md", "hash-a", model, vecA); err != nil {
		t.Fatalf("StoreEmbedding A: %v", err)
	}
	if err := idx.StoreEmbedding("page-b.md", "hash-b", model, vecB); err != nil {
		t.Fatalf("StoreEmbedding B: %v", err)
	}
	if err := idx.StoreEmbedding("page-c.md", "hash-c", model, vecC); err != nil {
		t.Fatalf("StoreEmbedding C: %v", err)
	}

	idx.SetEmbeddingModel(model)
	results, err := idx.SearchSimilar("page-a.md", 2)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results, got 0")
	}
	if results[0].Path != "page-b.md" {
		t.Errorf("most similar to A = %s, want page-b.md (Go topic)", results[0].Path)
	}
}

func TestSearchSimilarFTSFallback(t *testing.T) {
	idx := setupTestIndex(t)
	defer idx.Close()

	idx.Add("doc1.md", "Go concurrency patterns with goroutines", map[string]string{"title": "Go concurrency", "type": "source", "category": "", "tags": ""})
	idx.Add("doc2.md", "Go concurrency and channel patterns", map[string]string{"title": "Go concurrency channels", "type": "source", "category": "", "tags": ""})

	results, err := idx.SearchSimilar("doc1.md", 5)
	if err != nil {
		t.Fatalf("SearchSimilar FTS fallback: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("FTS fallback returned 0 results")
	}
	if results[0].Path != "doc2.md" {
		t.Errorf("FTS fallback top result = %s, want doc2.md", results[0].Path)
	}
}

func TestStalePaths(t *testing.T) {
	idx := setupTestIndex(t)
	defer idx.Close()

	if err := idx.InitEmbeddingsTable(); err != nil {
		t.Fatalf("InitEmbeddingsTable: %v", err)
	}

	idx.Add("fresh.md", "content", map[string]string{"title": "Fresh", "type": "", "category": "", "tags": ""})
	idx.Add("stale.md", "old content", map[string]string{"title": "Stale", "type": "", "category": "", "tags": ""})
	idx.Add("missing.md", "no embed", map[string]string{"title": "Missing", "type": "", "category": "", "tags": ""})

	model := "test-model"
	freshMeta, _ := idx.GetMeta("fresh.md")
	if err := idx.StoreEmbedding("fresh.md", freshMeta.ContentHash, model, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := idx.StoreEmbedding("stale.md", "wrong-hash", model, []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}

	stale, err := idx.StalePaths(model)
	if err != nil {
		t.Fatalf("StalePaths: %v", err)
	}

	paths := map[string]bool{}
	for _, s := range stale {
		paths[s.Path] = true
	}
	if paths["fresh.md"] {
		t.Error("fresh.md should not be stale")
	}
	if !paths["stale.md"] {
		t.Error("stale.md should be stale (hash mismatch)")
	}
	if !paths["missing.md"] {
		t.Error("missing.md should be stale (no embedding)")
	}
}

func TestEmbeddingsTableSurvivesSchemaRecreate(t *testing.T) {
	idx := setupTestIndex(t)
	defer idx.Close()

	if err := idx.InitEmbeddingsTable(); err != nil {
		t.Fatal(err)
	}
	if err := idx.StoreEmbedding("test.md", "hash1", "model1", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}

	count, err := idx.EmbeddingCount("model1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	if err := idx.initSchema(); err != nil {
		t.Fatalf("re-init schema: %v", err)
	}

	count, err = idx.EmbeddingCount("model1")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count after schema re-init = %d, want 1 (embeddings should survive)", count)
	}
}

func normalize(v []float32) {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
}

func setupTestIndex(t *testing.T) *SQLiteIndex {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	cfg := &config.Config{
		WikiDir:             t.TempDir(),
		DBPath:              dbPath,
		ConsistencyInterval: 0,
	}
	cfg.Exclude = []string{}
	idx, err := NewSQLiteIndex(cfg.WikiDir, dbPath, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	return idx
}
