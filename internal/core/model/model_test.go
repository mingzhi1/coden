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
