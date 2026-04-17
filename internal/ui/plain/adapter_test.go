package plain

import (
	"strings"
	"testing"
	"time"

	"github.com/mingzhi1/coden/internal/core/model"
)

func TestRenderSessionList(t *testing.T) {
	t.Parallel()

	out := New().RenderSessionList([]model.Session{
		{
			ID:          "sess-1",
			ProjectID:   "project-1",
			ProjectRoot: "/tmp/project-1",
			CreatedAt:   time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC),
		},
	})

	for _, want := range []string{"sessions:", "sess-1", "project=project-1", "root=/tmp/project-1", "created=2026-03-27T08:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got %q", want, out)
		}
	}
}

func TestRenderSessionListEmpty(t *testing.T) {
	t.Parallel()

	if got := New().RenderSessionList(nil); got != "no sessions" {
		t.Fatalf("unexpected output: %q", got)
	}
}
