package journeys

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// StubReply is the text the stub answers a main-model turn with: distinctive
// enough for a journey to find it on the harness's screen.
const StubReply = "stub-reply-ok"

// stubTitleReply answers a small-model request (opencode's session title), so a
// title on screen cannot pass for the turn's reply.
const stubTitleReply = "stub-title"

// ToolCall is a function call the stub answers a turn with: the tool's name and
// its JSON arguments, in the harness's own tool vocabulary.
type ToolCall struct {
	Name      string
	Arguments string
}

// stubToolCallID is the id of the stub's one tool call per turn.
const stubToolCallID = "call_stub"

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

// serveChatCompletion answers an OpenAI chat completion with call when one is
// given, else with the reply for its model; streamed as server-sent chunks when
// the request asks for a stream (opencode's AI SDK provider does) and as one
// JSON completion otherwise.
func serveChatCompletion(response http.ResponseWriter, request chatRequest, call *ToolCall) {
	answer := textAnswer(replyFor(request.Model))
	if call != nil {
		answer = toolCallAnswer(*call)
	}
	if request.Stream {
		streamChatCompletion(response, answer.delta, answer.finishReason)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(response,
		`{"id":"stub","object":"chat.completion","created":0,"model":%q,`+
			`"choices":[{"index":0,"message":%s,"finish_reason":%q}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		stubProviderModel, answer.message, answer.finishReason)
}

// chatAnswer is one answer in both shapes: the JSON message of a completion and
// the delta of a streamed chunk, with the finish reason they share.
type chatAnswer struct {
	message      string
	delta        string
	finishReason string
}

// textAnswer is a text reply.
func textAnswer(reply string) chatAnswer {
	message := fmt.Sprintf(`{"role":"assistant","content":%q}`, reply)
	return chatAnswer{message: message, delta: message, finishReason: "stop"}
}

// toolCallAnswer is a tool call.
func toolCallAnswer(call ToolCall) chatAnswer {
	function := fmt.Sprintf(`{"name":%q,"arguments":%q}`, call.Name, call.Arguments)
	message := fmt.Sprintf(
		`{"role":"assistant","content":null,"tool_calls":[{"id":%q,"type":"function","function":%s}]}`,
		stubToolCallID, function)
	delta := fmt.Sprintf(
		`{"role":"assistant","tool_calls":[{"index":0,"id":%q,"type":"function","function":%s}]}`,
		stubToolCallID, function)
	return chatAnswer{message: message, delta: delta, finishReason: "tool_calls"}
}

// streamChatCompletion writes delta as one chunk, a finish chunk, and the
// [DONE] sentinel.
func streamChatCompletion(response http.ResponseWriter, delta, finishReason string) {
	response.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta, finishReason string) string {
		return fmt.Sprintf(
			`{"id":"stub","object":"chat.completion.chunk","created":0,"model":%q,`+
				`"choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`,
			stubProviderModel, delta, finishReason)
	}
	events := []string{chunk(delta, "null"), chunk(`{}`, strconv.Quote(finishReason)), "[DONE]"}
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
