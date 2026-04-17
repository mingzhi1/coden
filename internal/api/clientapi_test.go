package api_test

import (
	"context"
	"testing"

	"github.com/mingzhi1/coden/internal/api"
	"github.com/mingzhi1/coden/internal/core/kernel"
)

func TestServiceImplementsClientAPI(t *testing.T) {
	// A compile-time check to ensure Service implements ClientAPI,
	// verifying that all objectstore and other methods are properly exposed via API layer.
	var _ api.ClientAPI = (*api.Service)(nil)
}

func TestAPIObjectStoreRouting(t *testing.T) {
	// In MVP, api.Service wraps kernel 1:1. Testing the routing semantics.
	// We instantiate an empty local kernel.
	k := kernel.New(t.TempDir())
	svc := api.New(k)

	objects, err := svc.ListWorkflowRunObjects(context.Background(), "session-1", "nonexistent-wf")
	if err == nil {
		t.Fatalf("ListWorkflowRunObjects expected error for nonexistent workflow, got nil")
	}
	if len(objects) != 0 {
		t.Fatalf("expected 0 objects, got %d", len(objects))
	}

	// ReadWorkflowRunObject for nonexistent object should fail
	_, err = svc.ReadWorkflowRunObject(context.Background(), "session-1", "wf-1", "obj-none")
	if err == nil {
		t.Fatalf("expected error reading nonexistent object")
	}
}
