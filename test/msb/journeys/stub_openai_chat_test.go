package journeys

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

type decodedCompletion struct {
	Object  string `json:"object"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func postChat(t *testing.T, provider StubProvider, body string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, stubLoopbackURL(t, provider)+"/v1/chat/completions", strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("POST /v1/chat/completions: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// stubLoopbackURL is the listener's real loopback URL: the advertised origin
// names a host only the VM resolves, so a host-side test dials the listener.
func stubLoopbackURL(t *testing.T, provider StubProvider) string {
	t.Helper()
	concrete, ok := provider.(*stubProvider)
	if !ok {
		t.Fatalf("provider is %T, want *stubProvider", provider)
	}
	return "http://" + concrete.listener.Addr().String()
}

func startTestStub(t *testing.T) StubProvider {
	t.Helper()
	provider, err := StartStubProvider(t)
	if err != nil {
		t.Fatalf("StartStubProvider: %v", err)
	}
	return provider
}

func TestStubAnswersAChatCompletionWithTheReply(t *testing.T) {
	provider := startTestStub(t)

	response := postChat(t, provider, `{"model":"stub","tools":[{}],"messages":[{"role":"user","content":"hello"}]}`)

	var completion decodedCompletion
	if err := json.NewDecoder(response.Body).Decode(&completion); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if completion.Object != "chat.completion" || len(completion.Choices) != 1 {
		t.Fatalf("completion = %+v, want one chat.completion choice", completion)
	}
	choice := completion.Choices[0]
	if choice.Message.Role != "assistant" || choice.Message.Content != stubReply(1) {
		t.Errorf("message = %+v, want the assistant reply %q", choice.Message, stubReply(1))
	}
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice.FinishReason)
	}
}

// opencode's AI SDK provider streams: the stub answers a stream request with
// server-sent chunks carrying the reply, a stop chunk, and the [DONE] sentinel.
func TestStubStreamsAChatCompletionWhenAsked(t *testing.T) {
	provider := startTestStub(t)

	response := postChat(
		t,
		provider,
		`{"model":"stub","stream":true,"tools":[{}],"messages":[{"role":"user","content":"hello"}]}`,
	)

	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	var content strings.Builder
	var stopped, done bool
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			done = true
			continue
		}
		var chunk decodedCompletion
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("decode chunk %q: %v", data, err)
		}
		if chunk.Object != "chat.completion.chunk" || len(chunk.Choices) != 1 {
			t.Fatalf("chunk = %q, want one chat.completion.chunk choice", data)
		}
		content.WriteString(chunk.Choices[0].Delta.Content)
		if reason := chunk.Choices[0].FinishReason; reason != nil && *reason == "stop" {
			stopped = true
		}
	}
	if content.String() != stubReply(1) || !stopped || !done {
		t.Errorf("stream content = %q, stopped = %v, done = %v; want %q, a stop chunk, and [DONE]",
			content.String(), stopped, done, stubReply(1))
	}
}

func TestStubRecordsTheRequestsItServed(t *testing.T) {
	provider := startTestStub(t)
	if requests := provider.Requests(); len(requests) != 0 {
		t.Fatalf("Requests() before any request = %v, want none", requests)
	}

	_, _ = io.Copy(io.Discard, postChat(t, provider, `{"model":"stub","tools":[{}],"messages":[]}`).Body)

	want := []StubRequest{{Method: http.MethodPost, Path: "/v1/chat/completions", Model: "stub"}}
	if got := provider.Requests(); !slices.Equal(got, want) {
		t.Errorf("Requests() = %v, want %v", got, want)
	}
}

func TestStubServedTurnCountsOnlyMainModelCompletions(t *testing.T) {
	models := StubRequest{Method: http.MethodGet, Path: "/v1/models"}
	title := StubRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Model: stubTitleModel, Side: true}
	turn := StubRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Model: stubProviderModel}
	if !servedTurn([]StubRequest{models, title, turn}) {
		t.Error("servedTurn = false with a main-model chat completion")
	}
	if servedTurn([]StubRequest{models, title}) {
		t.Error("servedTurn = true with only a model list and a title request")
	}
}

// opencode asks its small model for the session title; that answer must not
// read as the turn's reply.
func TestStubAnswersTheTitleModelWithADifferentReply(t *testing.T) {
	provider := startTestStub(t)

	response := postChat(t, provider, `{"model":"`+stubTitleModel+`","messages":[]}`)

	var completion decodedCompletion
	if err := json.NewDecoder(response.Body).Decode(&completion); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if got := completion.Choices[0].Message.Content; strings.HasPrefix(got, stubReplyPrefix) || got != stubTitleReply {
		t.Errorf("title reply = %q, want %q", got, stubTitleReply)
	}
}

type toolCallCompletion struct {
	Choices []struct {
		Message struct {
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		Delta struct {
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

var writeCall = ToolCall{Name: "write", Arguments: `{"filePath":"/workspace/x.txt","content":"hi"}`}

func TestStubAnswersTheNextTurnWithTheScriptedToolCallOnce(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(writeCall)

	var first toolCallCompletion
	if err := json.NewDecoder(postChat(t, provider, `{"model":"stub","tools":[{}],"messages":[]}`).Body).
		Decode(&first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	calls := first.Choices[0].Message.ToolCalls
	if len(calls) != 1 || calls[0].Function.Name != "write" || calls[0].Function.Arguments != writeCall.Arguments ||
		calls[0].Type != "function" || calls[0].ID == "" {
		t.Fatalf("tool calls = %+v, want the scripted write call", calls)
	}
	if reason := first.Choices[0].FinishReason; reason == nil || *reason != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", reason)
	}

	var second decodedCompletion
	if err := json.NewDecoder(postChat(t, provider, `{"model":"stub","tools":[{}],"messages":[]}`).Body).
		Decode(&second); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := second.Choices[0].Message.Content; got != stubReply(1) {
		t.Errorf("the turn after the tool call = %q, want the text reply", got)
	}
}

func TestStubStreamsTheScriptedToolCall(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(writeCall)

	response := postChat(t, provider, `{"model":"stub","stream":true,"tools":[{}],"messages":[]}`)

	var name, arguments strings.Builder
	var finished bool
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk toolCallCompletion
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("decode chunk %q: %v", data, err)
		}
		for _, call := range chunk.Choices[0].Delta.ToolCalls {
			name.WriteString(call.Function.Name)
			arguments.WriteString(call.Function.Arguments)
		}
		if reason := chunk.Choices[0].FinishReason; reason != nil && *reason == "tool_calls" {
			finished = true
		}
	}
	if name.String() != "write" || arguments.String() != writeCall.Arguments || !finished {
		t.Errorf("streamed call = %q(%s), finished = %v; want the scripted call and a tool_calls finish",
			name.String(), arguments.String(), finished)
	}
}

// The title model never consumes the scripted call: it belongs to the turn.
func TestStubKeepsTheScriptedToolCallFromTheTitleModel(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(writeCall)

	_, _ = io.Copy(io.Discard, postChat(t, provider, `{"model":"`+stubTitleModel+`","messages":[]}`).Body)

	var turn toolCallCompletion
	if err := json.NewDecoder(postChat(t, provider, `{"model":"stub","tools":[{}],"messages":[]}`).Body).
		Decode(&turn); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(turn.Choices[0].Message.ToolCalls) != 1 {
		t.Errorf("turn = %+v, want the scripted call after a title request", turn)
	}
}

func TestStubNumbersItsRepliesAndRecordsThem(t *testing.T) {
	provider := startTestStub(t)
	for range 2 {
		_, _ = io.Copy(io.Discard, postChat(t, provider, `{"model":"stub","tools":[{}],"messages":[]}`).Body)
	}
	_, _ = io.Copy(io.Discard, postChat(t, provider, `{"model":"`+stubTitleModel+`","messages":[]}`).Body)

	if got, want := provider.Replies(), []string{stubReply(1), stubReply(2)}; !slices.Equal(got, want) {
		t.Errorf("Replies() = %v, want %v (title replies excluded)", got, want)
	}
}

func TestStubServedTurnCountsAnthropicMessages(t *testing.T) {
	if !servedTurn([]StubRequest{{Method: http.MethodPost, Path: "/v1/messages", Model: "stub"}}) {
		t.Error("servedTurn = false for a Messages turn")
	}
}

func TestStubServedTurnCountsResponses(t *testing.T) {
	if !servedTurn([]StubRequest{{Method: http.MethodPost, Path: "/v1/responses", Model: "stub"}}) {
		t.Error("servedTurn = false for a Responses turn")
	}
}

// codex asks its main model for the session title without tools; that side
// request must not take the armed call or a numbered reply.
func TestStubTreatsAToolLessRequestAsASideRequest(t *testing.T) {
	provider := startTestStub(t)
	provider.ScriptToolCall(writeCall)

	var title decodedCompletion
	if err := json.NewDecoder(postChat(t, provider, `{"model":"stub","messages":[]}`).Body).Decode(&title); err != nil {
		t.Fatal(err)
	}
	if got := title.Choices[0].Message.Content; got != stubTitleReply {
		t.Errorf("tool-less reply = %q, want %q", got, stubTitleReply)
	}
	if len(provider.Replies()) != 0 || servedTurn(provider.Requests()) {
		t.Error("a tool-less request counted as a turn")
	}
}

// TestSideRequestRuleCoversEachHarnesssRequestKinds pins, per harness, which of
// the rule's three request shapes (a turn with tools, a small-model request, a
// tool-less main-model request) its side requests and turns took on a KVM host,
// and that every harness in core-session is listed. Login-only journeys do not
// issue a model prompt and need no provider request shape. The shapes are the rule's, not
// captured bodies: a harness whose side request carries tools on the main
// model, or whose turn carries none, breaks the rule and must be configured
// around it (as oh-my-pi's adapter is) before the rule changes.
func TestSideRequestRuleCoversEachHarnesssRequestKinds(t *testing.T) {
	const (
		turn       = `{"model":"stub","stream":true,"tools":[{"type":"function"}]}`
		smallModel = `{"model":"stub-title","stream":true,"messages":[]}`
		toolLess   = `{"model":"stub","stream":true,"messages":[]}`
	)
	observed := map[string]struct{ side, turns []string }{
		opencodeAgentName:   {side: []string{smallModel}, turns: []string{turn}},
		opencode2AgentName:  {side: []string{smallModel}, turns: []string{turn}},
		claudeCodeAgentName: {side: []string{smallModel}, turns: []string{turn}},
		codexAgentName:      {side: []string{toolLess}, turns: []string{turn}},
		piAgentName:         {side: nil, turns: []string{turn}},
		ohMyPiAgentName:     {side: []string{toolLess, smallModel}, turns: []string{turn}},
		hermesAgentName:     {side: []string{toolLess}, turns: []string{turn}},
	}
	for name, requests := range observed {
		for _, body := range requests.side {
			var request chatRequest
			if err := json.Unmarshal([]byte(body), &request); err != nil || !request.side() {
				t.Errorf("%s side request %s: side() = false (%v)", name, body, err)
			}
		}
		for _, body := range requests.turns {
			var request chatRequest
			if err := json.Unmarshal([]byte(body), &request); err != nil || request.side() {
				t.Errorf("%s turn %s: side() = true (%v)", name, body, err)
			}
		}
	}
	for _, name := range coreSessionJourney.Harnesses {
		if _, ok := observed[name]; !ok {
			t.Errorf("core-session drives %s, whose side and turn requests are not pinned here", name)
		}
	}
}
