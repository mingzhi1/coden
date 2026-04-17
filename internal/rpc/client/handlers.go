package client

import (
	"encoding/json"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/rpc/protocol"
)

func (c *Client) handleResponse(resp protocol.Response) {
	var id int64
	if resp.ID != nil {
		json.Unmarshal(*resp.ID, &id)
	}

	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()

	if !ok {
		return
	}

	if resp.Error != nil {
		ch <- callResult{err: resp.Error}
		return
	}

	ch <- callResult{raw: resp.Result}
}

func (c *Client) handleNotification(notif protocol.Notification) {
	if notif.Method != protocol.MethodEventPush {
		return
	}

	var event model.Event
	if err := json.Unmarshal(notif.Params, &event); err != nil {
		return
	}

	c.subMu.Lock()
	defer c.subMu.Unlock()

	// Deliver to session-specific subscribers
	for _, ch := range c.subs[event.SessionID] {
		select {
		case ch <- event:
		default: // drop if subscriber is slow
		}
	}

	// Deliver to wildcard subscribers (empty session)
	for _, ch := range c.subs[""] {
		select {
		case ch <- event:
		default:
		}
	}
}
