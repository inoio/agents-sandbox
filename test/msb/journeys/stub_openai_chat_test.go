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

type chatCompletion struct {
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

	response := postChat(t, provider, `{"model":"stub","messages":[{"role":"user","content":"hello"}]}`)

	var completion chatCompletion
	if err := json.NewDecoder(response.Body).Decode(&completion); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if completion.Object != "chat.completion" || len(completion.Choices) != 1 {
		t.Fatalf("completion = %+v, want one chat.completion choice", completion)
	}
	choice := completion.Choices[0]
	if choice.Message.Role != "assistant" || choice.Message.Content != StubReply {
		t.Errorf("message = %+v, want the assistant reply %q", choice.Message, StubReply)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice.FinishReason)
	}
}

// opencode's AI SDK provider streams: the stub answers a stream request with
// server-sent chunks carrying the reply, a stop chunk, and the [DONE] sentinel.
func TestStubStreamsAChatCompletionWhenAsked(t *testing.T) {
	provider := startTestStub(t)

	response := postChat(t, provider, `{"model":"stub","stream":true,"messages":[{"role":"user","content":"hello"}]}`)

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
		var chunk chatCompletion
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
	if content.String() != StubReply || !stopped || !done {
		t.Errorf("stream content = %q, stopped = %v, done = %v; want %q, a stop chunk, and [DONE]",
			content.String(), stopped, done, StubReply)
	}
}

func TestStubRecordsTheRequestsItServed(t *testing.T) {
	provider := startTestStub(t)
	if requests := provider.Requests(); len(requests) != 0 {
		t.Fatalf("Requests() before any request = %v, want none", requests)
	}

	_, _ = io.Copy(io.Discard, postChat(t, provider, `{"model":"stub","messages":[]}`).Body)

	want := []StubRequest{{Method: http.MethodPost, Path: "/v1/chat/completions", Model: "stub"}}
	if got := provider.Requests(); !slices.Equal(got, want) {
		t.Errorf("Requests() = %v, want %v", got, want)
	}
}

func TestStubServedTurnCountsOnlyMainModelCompletions(t *testing.T) {
	models := StubRequest{Method: http.MethodGet, Path: "/v1/models"}
	title := StubRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Model: stubTitleModel}
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

	var completion chatCompletion
	if err := json.NewDecoder(response.Body).Decode(&completion); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if got := completion.Choices[0].Message.Content; got == StubReply || !strings.Contains(got, "title") {
		t.Errorf("title reply = %q, want a reply other than %q", got, StubReply)
	}
}
