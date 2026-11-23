package journeys

import (
	"fmt"
	"net/http"
	"strings"
)

// StubReply is the text the stub answers a main-model turn with: distinctive
// enough for a journey to find it on the harness's screen.
const StubReply = "stub-reply-ok"

// stubTitleReply answers a small-model request (opencode's session title), so a
// title on screen cannot pass for the turn's reply.
const stubTitleReply = "stub-title"

// chatRequest is the part of a chat completion request the stub reads.
type chatRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// replyFor is the reply text for a request's model.
func replyFor(model string) string {
	if model == stubTitleModel {
		return stubTitleReply
	}
	return StubReply
}

// stubCompletionPath is the request path of an OpenAI chat completion.
const stubCompletionPath = "/chat/completions"

// serveChatCompletion answers an OpenAI chat completion with the reply for its
// model, streamed as server-sent chunks when the request asks for a stream
// (opencode's AI SDK provider does) and as one JSON completion otherwise.
func serveChatCompletion(response http.ResponseWriter, request chatRequest) {
	reply := replyFor(request.Model)
	if request.Stream {
		streamChatCompletion(response, reply)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(response,
		`{"id":"stub","object":"chat.completion","created":0,"model":%q,`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		stubProviderModel, reply)
}

// streamChatCompletion writes the reply as one content chunk, a stop chunk, and
// the [DONE] sentinel.
func streamChatCompletion(response http.ResponseWriter, reply string) {
	response.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta, finishReason string) string {
		return fmt.Sprintf(
			`{"id":"stub","object":"chat.completion.chunk","created":0,"model":%q,`+
				`"choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`,
			stubProviderModel, delta, finishReason)
	}
	events := []string{
		chunk(fmt.Sprintf(`{"role":"assistant","content":%q}`, reply), "null"),
		chunk(`{}`, `"stop"`),
		"[DONE]",
	}
	for _, event := range events {
		_, _ = fmt.Fprintf(response, "data: %s\n\n", event)
	}
}

// servedTurn reports whether requests include a main-model chat completion, the
// request a harness sends for a turn (a small-model title request is not one).
func servedTurn(requests []StubRequest) bool {
	for _, request := range requests {
		if request.Method == http.MethodPost && strings.HasSuffix(request.Path, stubCompletionPath) &&
			request.Model != stubTitleModel {
			return true
		}
	}
	return false
}
