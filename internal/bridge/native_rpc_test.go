package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNativeRPCDefersOverlappingImageCallbacks(t *testing.T) {
	rpc, wire := scriptedRPC(t)
	for _, raw := range []string{
		`{"id":"second-tool","method":"item/tool/call","params":{}}`,
		`{"method":"item/agentMessage/delta","params":{"delta":"early"}}`,
		`{"id":1,"result":{"turnId":"turn"}}`,
		`{"id":2,"result":{"turnId":"turn"}}`,
		`{"method":"turn/completed"}`,
	} {
		rpc.packets <- conversationPacket{Data: []byte(raw)}
	}
	content := []any{map[string]any{"type": "inputText", "text": `{"savedId":"committed"}`}, map[string]any{"type": "inputImage", "imageUrl": "data:image/png;base64,eA=="}}
	calls := 0
	rpc.toolHandler = func(ctx context.Context, m nativeMessage) error {
		calls++
		// The first callback must already be acknowledged before this one
		// can send another steer request.
		if !strings.Contains(wire.String(), `"id":"first-tool","result"`) {
			t.Fatal("nested callback dispatched before its predecessor completed")
		}
		result, err := deliverCodexToolImages(ctx, rpc, "thread", "turn", "second", content)
		if err != nil || len(result) != 1 {
			t.Fatal("second callback lost its acknowledgement", result, err)
		}
		return rpc.send(map[string]any{"id": m.ID, "result": map[string]any{"contentItems": result, "success": true}})
	}
	result, err := deliverCodexToolImages(t.Context(), rpc, "thread", "turn", "first", content)
	if err != nil || len(result) != 1 || calls != 0 {
		t.Fatal("first acknowledgement was stolen or callback ran recursively", result, calls, err)
	}
	if err := rpc.send(map[string]any{"id": "first-tool", "result": map[string]any{"contentItems": result, "success": true}}); err != nil {
		t.Fatal(err)
	}
	event, err := rpc.receive(t.Context())
	if err != nil || calls != 1 || event.Method != "item/agentMessage/delta" {
		t.Fatal("queued callback or notification lost", calls, event, err)
	}
	event, err = rpc.receive(t.Context())
	if err != nil || event.Method != "turn/completed" {
		t.Fatal("completion lost", event, err)
	}
	lines := strings.Split(strings.TrimSpace(wire.String()), "\n")
	if len(lines) != 4 {
		t.Fatal("unexpected RPCs or duplicated callback results", wire.String())
	}
}

func TestNativeRPCToolBeforeTurnStartResponse(t *testing.T) {
	rpc, _ := scriptedRPC(t)
	rpc.packets <- conversationPacket{Data: []byte(`{"id":"early-tool","method":"item/tool/call"}`)}
	rpc.packets <- conversationPacket{Data: []byte(`{"id":1,"result":{"turnId":"bound-turn"}}`)}
	rpc.packets <- conversationPacket{Data: []byte(`{"method":"turn/completed"}`)}
	bound := ""
	calls := 0
	rpc.toolHandler = func(context.Context, nativeMessage) error {
		calls++
		if bound != "bound-turn" {
			t.Fatal("callback ran before turn ownership was bound")
		}
		return nil
	}
	var response struct {
		TurnID string `json:"turnId"`
	}
	if err := rpc.call(t.Context(), "turn/start", nil, &response); err != nil || calls != 0 {
		t.Fatal("early callback was not deferred", err)
	}
	bound = response.TurnID
	if _, err := rpc.receive(t.Context()); err != nil || calls != 1 {
		t.Fatal("early callback lost", err)
	}
}

func TestNativeRPCDeferredRequestsStayBoundedAndCancellable(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		rpc, _ := scriptedRPC(t)
		rpc.toolHandler = func(context.Context, nativeMessage) error { t.Fatal("cancelled callback ran"); return nil }
		rpc.notifications = []nativeMessage{{ID: json.RawMessage(`"tool"`), Method: "item/tool/call"}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := rpc.receive(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation ignored", err)
		}
	})
	t.Run("bounded", func(t *testing.T) {
		rpc, _ := scriptedRPC(t)
		rpc.notifications = make([]nativeMessage, 128)
		rpc.toolHandler = func(context.Context, nativeMessage) error { t.Fatal("callback ran inside RPC"); return nil }
		rpc.packets <- conversationPacket{Data: []byte(`{"id":"tool","method":"item/tool/call"}`)}
		if err := rpc.call(t.Context(), "turn/steer", nil, nil); err == nil {
			t.Fatal("unbounded callback queue")
		}
	})
	for _, raw := range []string{
		`{"id":"unknown","method":"account/login"}`,
		`{"id":2,"result":{}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			rpc, _ := scriptedRPC(t)
			rpc.toolHandler = func(context.Context, nativeMessage) error { t.Fatal("unexpected callback"); return nil }
			rpc.packets <- conversationPacket{Data: []byte(raw)}
			if err := rpc.call(t.Context(), "turn/steer", nil, nil); err == nil {
				t.Fatal("foreign response/request accepted")
			}
		})
	}
}
