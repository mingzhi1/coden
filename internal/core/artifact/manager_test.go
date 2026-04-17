package artifact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestManager(t *testing.T) (Manager, string) {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "artifacts")
	mgr, err := NewManager(dataDir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr, dir
}

func TestSaveAndGetToolResult(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	tc := ToolCall{
		ToolKind:    "run_shell",
		RequestJSON: `{"command":"go test ./..."}`,
		Status:      StatusSuccess,
		DurationMs:  150,
	}

	sr, err := mgr.SaveToolResult(ctx, "wf-1", "sess-1", tc, "PASS\nok  pkg 0.5s", "some warning", "", "")
	if err != nil {
		t.Fatalf("SaveToolResult: %v", err)
	}
	if sr.ToolCallID == "" {
		t.Fatal("expected non-empty ToolCallID")
	}
	if len(sr.Artifacts) != 2 {
		t.Fatalf("expected 2 artifacts (output + stderr), got %d", len(sr.Artifacts))
	}
	if sr.Primary == nil {
		t.Fatal("expected non-nil Primary artifact")
	}

	// Read back the primary artifact.
	content, err := mgr.GetArtifactContent(ctx, sr.Primary.ID)
	if err != nil {
		t.Fatalf("GetArtifactContent: %v", err)
	}
	if !strings.Contains(string(content), "PASS") {
		t.Errorf("expected content to contain PASS, got %q", string(content))
	}
}

func TestSaveAndGetLargeContent(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	// Create content larger than InlineSizeThreshold.
	bigData := make([]byte, InlineSizeThreshold+1024)
	for i := range bigData {
		bigData[i] = byte('A' + (i % 26))
	}

	a, err := mgr.SaveContent(ctx, "wf-2", "sess-2", "", KindSpill, "big_output", bigData, nil)
	if err != nil {
		t.Fatalf("SaveContent: %v", err)
	}
	if a.BlobID == "" {
		t.Fatal("expected non-empty BlobID for large content")
	}
	if len(a.Content) != 0 {
		t.Fatal("expected empty inline Content for large artifact")
	}

	// Read back.
	got, err := mgr.GetArtifactContent(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArtifactContent: %v", err)
	}
	if len(got) != len(bigData) {
		t.Fatalf("content size mismatch: want %d, got %d", len(bigData), len(got))
	}
}

func TestListAndFind(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	// Save a few artifacts across workflows.
	for i := 0; i < 3; i++ {
		tc := ToolCall{ToolKind: "search", Status: StatusSuccess}
		_, _ = mgr.SaveToolResult(ctx, "wf-list", "sess-list", tc, "result", "", "", "")
	}
	tc2 := ToolCall{ToolKind: "read_file", Status: StatusSuccess}
	_, _ = mgr.SaveToolResult(ctx, "wf-other", "sess-list", tc2, "file content", "", "", "")

	// List by workflow.
	arts, err := mgr.ListWorkflowArtifacts(ctx, "wf-list", ListOptions{})
	if err != nil {
		t.Fatalf("ListWorkflowArtifacts: %v", err)
	}
	if len(arts) != 3 {
		t.Fatalf("expected 3 artifacts for wf-list, got %d", len(arts))
	}

	// List by session (should include all 4).
	arts, err = mgr.ListSessionArtifacts(ctx, "sess-list", ListOptions{})
	if err != nil {
		t.Fatalf("ListSessionArtifacts: %v", err)
	}
	if len(arts) != 4 {
		t.Fatalf("expected 4 artifacts for sess-list, got %d", len(arts))
	}

	// Find by tool kind.
	arts, err = mgr.FindArtifacts(ctx, FindQuery{ToolKinds: []string{"read_file"}})
	if err != nil {
		t.Fatalf("FindArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Fatalf("expected 1 read_file artifact, got %d", len(arts))
	}
}

func TestReferences(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	a1, _ := mgr.SaveContent(ctx, "wf-ref", "sess-ref", "", KindToolOutput, "a1", []byte("content1"), nil)
	a2, _ := mgr.SaveContent(ctx, "wf-ref", "sess-ref", "", KindToolOutput, "a2", []byte("content2"), nil)

	if err := mgr.CreateReference(ctx, a2.ID, a1.ID, "reuse"); err != nil {
		t.Fatalf("CreateReference: %v", err)
	}

	// a2 references a1.
	referenced, err := mgr.GetReferencedArtifacts(ctx, a2.ID)
	if err != nil {
		t.Fatalf("GetReferencedArtifacts: %v", err)
	}
	if len(referenced) != 1 || referenced[0].ID != a1.ID {
		t.Fatalf("expected a2 to reference a1, got %v", referenced)
	}

	// a1 is referenced by a2.
	referencing, err := mgr.GetReferencingArtifacts(ctx, a1.ID)
	if err != nil {
		t.Fatalf("GetReferencingArtifacts: %v", err)
	}
	if len(referencing) != 1 || referencing[0].ID != a2.ID {
		t.Fatalf("expected a1 to be referenced by a2, got %v", referencing)
	}
}

func TestCleanupWorkflow(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	tc := ToolCall{ToolKind: "search", Status: StatusSuccess}
	_, _ = mgr.SaveToolResult(ctx, "wf-clean", "sess-clean", tc, "output", "", "", "")

	arts, _ := mgr.ListWorkflowArtifacts(ctx, "wf-clean", ListOptions{})
	if len(arts) == 0 {
		t.Fatal("expected artifacts before cleanup")
	}

	if err := mgr.CleanupWorkflow(ctx, "wf-clean"); err != nil {
		t.Fatalf("CleanupWorkflow: %v", err)
	}

	arts, _ = mgr.ListWorkflowArtifacts(ctx, "wf-clean", ListOptions{})
	if len(arts) != 0 {
		t.Fatalf("expected 0 artifacts after cleanup, got %d", len(arts))
	}
}

func TestCleanupBefore(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	tc := ToolCall{ToolKind: "search", Status: StatusSuccess}
	_, _ = mgr.SaveToolResult(ctx, "wf-old", "sess-old", tc, "old output", "", "", "")

	// Cleanup everything before 1 second from now (should remove the artifact).
	future := time.Now().Add(1 * time.Second)
	result, err := mgr.CleanupBefore(ctx, future, CleanupOptions{})
	if err != nil {
		t.Fatalf("CleanupBefore: %v", err)
	}
	if result.ArtifactsRemoved == 0 {
		t.Fatal("expected at least 1 artifact removed")
	}
}

func TestBlobStoreIdempotent(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	data := []byte("hello world")
	id1, existed1, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if existed1 {
		t.Fatal("expected first Put to report not-existed")
	}
	id2, existed2, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put (idempotent): %v", err)
	}
	if !existed2 {
		t.Fatal("expected second Put to report existed")
	}
	if id1 != id2 {
		t.Fatalf("expected same blob ID for same content, got %s vs %s", id1, id2)
	}

	got, err := bs.Get(id1)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("content mismatch")
	}

	if err := bs.Delete(id1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if bs.Exists(id1) {
		t.Fatal("expected blob to be deleted")
	}
}

func TestMetadataJSON(t *testing.T) {
	a := &Artifact{
		Metadata: map[string]string{"url": "https://example.com", "status": "200"},
	}
	j := a.MetadataJSON()
	if j == "" {
		t.Fatal("expected non-empty JSON")
	}

	a2 := &Artifact{}
	a2.ParseMetadataJSON(j)
	if a2.Metadata["url"] != "https://example.com" {
		t.Fatalf("metadata roundtrip failed: %v", a2.Metadata)
	}
}

// ─── Phase 3 tests ───────────────────────────────────────────────────

func TestCleanupSession(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	tc := ToolCall{ToolKind: "search", Status: StatusSuccess}
	_, _ = mgr.SaveToolResult(ctx, "wf-cs", "sess-cs", tc, "output", "", "", "")
	_, _ = mgr.SaveToolResult(ctx, "wf-cs", "sess-other", tc, "other", "", "", "")

	if err := mgr.CleanupSession(ctx, "sess-cs"); err != nil {
		t.Fatalf("CleanupSession: %v", err)
	}

	arts, _ := mgr.ListSessionArtifacts(ctx, "sess-cs", ListOptions{})
	if len(arts) != 0 {
		t.Fatalf("expected 0 after session cleanup, got %d", len(arts))
	}
	// Other session unaffected.
	arts, _ = mgr.ListSessionArtifacts(ctx, "sess-other", ListOptions{})
	if len(arts) != 1 {
		t.Fatalf("expected 1 for sess-other, got %d", len(arts))
	}
}

func TestRunGC(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	// Save a large artifact so it goes to blob store.
	bigData := make([]byte, InlineSizeThreshold+512)
	for i := range bigData {
		bigData[i] = byte('X')
	}
	a, err := mgr.SaveContent(ctx, "wf-gc", "sess-gc", "", KindSpill, "big", bigData, nil)
	if err != nil {
		t.Fatalf("SaveContent: %v", err)
	}
	if a.BlobID == "" {
		t.Fatal("expected blob storage for large content")
	}

	// Cleanup workflow → should decr ref-count.
	if err := mgr.CleanupWorkflow(ctx, "wf-gc"); err != nil {
		t.Fatalf("CleanupWorkflow: %v", err)
	}

	// Run GC → should remove orphan blob.
	gcRes, err := mgr.RunGC(ctx)
	if err != nil {
		t.Fatalf("RunGC: %v", err)
	}
	if gcRes.BlobsRemoved == 0 {
		t.Fatal("expected at least 1 blob freed by GC")
	}
}

func TestRunAutoCleanup_RetentionCount(t *testing.T) {
	mgr, _ := setupTestManager(t)
	ctx := context.Background()

	tc := ToolCall{ToolKind: "search", Status: StatusSuccess}
	// Create 5 workflows.
	for i := 0; i < 5; i++ {
		wfID := fmt.Sprintf("wf-auto-%d", i)
		_, _ = mgr.SaveToolResult(ctx, wfID, "sess-auto", tc, fmt.Sprintf("output-%d", i), "", "", "")
		// Small sleep to ensure ordering.
		time.Sleep(2 * time.Millisecond)
	}

	// Retain only 2 most recent → should remove 3.
	res, err := mgr.RunAutoCleanup(ctx, 2, 0)
	if err != nil {
		t.Fatalf("RunAutoCleanup: %v", err)
	}
	if res.ArtifactsRemoved == 0 {
		t.Fatal("expected some artifacts removed")
	}

	// Only 2 workflows should remain.
	arts, _ := mgr.ListSessionArtifacts(ctx, "sess-auto", ListOptions{})
	if len(arts) != 2 {
		t.Fatalf("expected 2 artifacts remaining, got %d", len(arts))
	}
}

func BenchmarkFind1000Artifacts(b *testing.B) {
	dir := b.TempDir()
	dataDir := filepath.Join(dir, "artifacts")
	mgr, err := NewManager(dataDir)
	if err != nil {
		b.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	tc := ToolCall{ToolKind: "run_shell", Status: StatusSuccess}
	for i := 0; i < 1000; i++ {
		wfID := fmt.Sprintf("wf-bench-%d", i%10)
		_, _ = mgr.SaveToolResult(ctx, wfID, "sess-bench", tc, fmt.Sprintf("output line %d", i), "", "", "")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mgr.FindArtifacts(ctx, FindQuery{SessionID: "sess-bench", Limit: 20})
	}
}

func TestBlobStoreDirectoryLayout(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	id, _, err := bs.Put([]byte("test content"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Check that the blob is stored under a two-character prefix directory.
	prefix := id[:2]
	expectedDir := filepath.Join(dir, "blobs", prefix)
	if _, err := os.Stat(expectedDir); err != nil {
		t.Fatalf("expected prefix dir %s to exist: %v", expectedDir, err)
	}
}
