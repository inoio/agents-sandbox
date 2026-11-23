package journeys

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

// responsesStream reads a Responses stream into its event types, the text
// deltas, and the completed output items.
func responsesStream(t *testing.T, provider StubProvider) (events []string, text string, items []json.RawMessage) {
	t.Helper()
	response := postMessages(t, provider, "/v1/responses", `{"model":"stub","stream":true,"tools":[{}],"input":[]}`)
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	var textBuilder strings.Builder
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var event struct {
			Type  string          `json:"type"`
			Delta string          `json:"delta"`
			Item  json.RawMessage `json:"item"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("decode event %q: %v", data, err)
		}
		events = append(events, event.Type)
		textBuilder.WriteString(event.Delta)
		if event.Type == "response.output_item.done" {
			items = append(items, event.Item)
		}
	}
	return events, textBuilder.String(), items
}

func TestStubStreamsAResponsesTextReply(t *testing.T) {
	provider := startTestStub(t)

	events, text, items := responsesStream(t, provider)

	if events[0] != "response.created" || events[len(events)-1] != "response.completed" {
		t.Errorf("events = %v, want response.created … response.completed", events)
	}
	if text != stubReply(1) || len(items) != 1 {
		t.Fatalf("text = %q, items = %s; want the reply and one message item", text, items)
	}
	var item struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(items[0], &item); err != nil {
		t.Fatal(err)
	}
	if item.Type != "message" || item.Role != "assistant" || len(item.Content) != 1 ||
		item.Content[0].Type != "output_text" || item.Content[0].Text != stubReply(1) {
		t.Errorf("item = %+v, want an assistant message with the reply as output_text", item)
	}
}

func TestStubStreamsTheScriptedFunctionCall(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(ToolCall{Name: "shell", Arguments: `{"command":["true"]}`})

	_, _, items := responsesStream(t, provider)

	var item struct {
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	if len(items) != 1 {
		t.Fatalf("items = %s, want one", items)
	}
	if err := json.Unmarshal(items[0], &item); err != nil {
		t.Fatal(err)
	}
	if item.Type != "function_call" || item.Name != "shell" || item.Arguments != `{"command":["true"]}` ||
		item.CallID == "" {
		t.Errorf("item = %+v, want the scripted function_call", item)
	}
}
