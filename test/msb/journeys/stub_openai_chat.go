package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// StubReply is the text every stub turn answers with: distinctive enough for a
// journey to find it on the harness's screen.
const StubReply = "stub-reply-ok"

// stubCompletionPath is the request path of an OpenAI chat completion.
const stubCompletionPath = "/chat/completions"

// serveChatCompletion answers an OpenAI chat completion with StubReply, streamed
// as server-sent chunks when the request asks for a stream (opencode's AI SDK
// provider does) and as one JSON completion otherwise. A body that does not
// decode is answered non-streamed.
func serveChatCompletion(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Stream bool `json:"stream"`
	}
	_ = json.NewDecoder(request.Body).Decode(&body)
	if body.Stream {
		streamChatCompletion(response)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(response,
		`{"id":"stub","object":"chat.completion","created":0,"model":%q,`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		stubProviderModel, StubReply)
}

// streamChatCompletion writes the reply as one content chunk, a stop chunk, and
// the [DONE] sentinel.
func streamChatCompletion(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta, finishReason string) string {
		return fmt.Sprintf(
			`{"id":"stub","object":"chat.completion.chunk","created":0,"model":%q,`+
				`"choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`,
			stubProviderModel, delta, finishReason)
	}
	events := []string{
		chunk(fmt.Sprintf(`{"role":"assistant","content":%q}`, StubReply), "null"),
		chunk(`{}`, `"stop"`),
		"[DONE]",
	}
	for _, event := range events {
		_, _ = fmt.Fprintf(response, "data: %s\n\n", event)
	}
}

// servedTurn reports whether requests include a chat completion, the request a
// harness sends for a turn.
func servedTurn(requests []StubRequest) bool {
	for _, request := range requests {
		if request.Method == http.MethodPost && strings.HasSuffix(request.Path, stubCompletionPath) {
			return true
		}
	}
	return false
}
