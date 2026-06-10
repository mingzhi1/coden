package model

import "testing"

func TestEncodePayloadPanicsOnMarshalError(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("expected EncodePayload to panic on marshal error")
		}
	}()

	type badPayload struct {
		Fn func()
	}

	_ = EncodePayload(badPayload{Fn: func() {}})
}

// TestIntentKindSingleSource guards the single source of truth for intent kinds:
// research must be canonical (so it is reachable end-to-end) and unknown kinds
// must be rejected.
func TestIntentKindSingleSource(t *testing.T) {
	if !IsKnownIntentKind(IntentKindResearch) {
		t.Error("research must be a canonical intent kind (else the research workflow is unreachable)")
	}
	for _, k := range AllIntentKinds {
		if !IsKnownIntentKind(k) {
			t.Errorf("canonical kind %q failed IsKnownIntentKind", k)
		}
	}
	if IsKnownIntentKind("nonsense") {
		t.Error("unknown kind should be rejected")
	}
}
