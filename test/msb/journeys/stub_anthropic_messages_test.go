package journeys

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func postMessages(t *testing.T, provider StubProvider, path, body string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, stubLoopbackURL(t, provider)+path, strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

type anthropicMessage struct {
	Type       string `json:"type"`
	Role       string `json:"role"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
}

func TestStubAnswersAnthropicMessagesWithTheReply(t *testing.T) {
	provider := startTestStub(t)

	var message anthropicMessage
	if err := json.NewDecoder(postMessages(t, provider, "/v1/messages", `{"model":"stub","tools":[{}],"messages":[]}`).Body).
		Decode(&message); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if message.Type != "message" || message.Role != "assistant" || message.StopReason != "end_turn" ||
		len(message.Content) != 1 || message.Content[0].Type != "text" || message.Content[0].Text != stubReply(1) {
		t.Errorf("message = %+v, want one text block %q ending the turn", message, stubReply(1))
	}
}

func TestStubAnswersAnthropicMessagesWithTheScriptedToolUse(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(ToolCall{Name: "Write", Arguments: `{"file_path":"/workspace/a.txt","content":"hi"}`})

	var message anthropicMessage
	if err := json.NewDecoder(postMessages(t, provider, "/v1/messages", `{"model":"stub","tools":[{}],"messages":[]}`).Body).
		Decode(&message); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if message.StopReason != "tool_use" || len(message.Content) != 1 || message.Content[0].Type != "tool_use" ||
		message.Content[0].Name != "Write" || message.Content[0].ID == "" ||
		string(message.Content[0].Input) != `{"file_path":"/workspace/a.txt","content":"hi"}` {
		t.Errorf("message = %+v, want the scripted Write tool_use", message)
	}
}

// anthropicStreamEvents reads a Messages stream into its event types and the
// text and tool-input it carried.
func anthropicStreamEvents(t *testing.T, body io.Reader) (events []string, text, toolInput, stopReason string) {
	t.Helper()
	var textBuilder, inputBuilder strings.Builder
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("decode event %q: %v", data, err)
		}
		events = append(events, event.Type)
		textBuilder.WriteString(event.Delta.Text)
		inputBuilder.WriteString(event.Delta.PartialJSON)
		if event.Delta.StopReason != "" {
			stopReason = event.Delta.StopReason
		}
	}
	return events, textBuilder.String(), inputBuilder.String(), stopReason
}

func TestStubStreamsAnthropicMessages(t *testing.T) {
	provider := startTestStub(t)

	response := postMessages(t, provider, "/v1/messages", `{"model":"stub","stream":true,"tools":[{}],"messages":[]}`)

	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	events, text, _, stopReason := anthropicStreamEvents(t, response.Body)
	want := "message_start,content_block_start,content_block_delta,content_block_stop,message_delta,message_stop"
	if strings.Join(events, ",") != want || text != stubReply(1) || stopReason != "end_turn" {
		t.Errorf("events = %v, text = %q, stop = %q; want %s with the reply", events, text, stopReason, want)
	}
}

func TestStubStreamsTheScriptedToolUse(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(ToolCall{Name: "Write", Arguments: `{"file_path":"/workspace/a.txt","content":"hi"}`})

	response := postMessages(t, provider, "/v1/messages", `{"model":"stub","stream":true,"tools":[{}],"messages":[]}`)

	_, _, input, stopReason := anthropicStreamEvents(t, response.Body)
	if input != `{"file_path":"/workspace/a.txt","content":"hi"}` || stopReason != "tool_use" {
		t.Errorf("tool input = %q, stop = %q; want the scripted input and tool_use", input, stopReason)
	}
}

func TestStubCountsTokensWithoutTakingATurn(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(ToolCall{Name: "Write", Arguments: `{}`})

	var count struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.NewDecoder(postMessages(t, provider, "/v1/messages/count_tokens", `{"model":"stub"}`).Body).
		Decode(&count); err != nil || count.InputTokens == 0 {
		t.Fatalf("count_tokens = %+v, %v; want a token count", count, err)
	}
	if len(provider.Replies()) != 0 || servedTurn(provider.Requests()) {
		t.Error("a token count took a turn")
	}
	var turn anthropicMessage
	_ = json.NewDecoder(postMessages(t, provider, "/v1/messages", `{"model":"stub","tools":[{}]}`).Body).Decode(&turn)
	if turn.StopReason != "tool_use" {
		t.Errorf("the first real turn = %+v, want the still-armed tool call", turn)
	}
}
