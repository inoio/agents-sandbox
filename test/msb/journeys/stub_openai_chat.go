package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// stubReplyPrefix starts every main-model text reply; the stub numbers its
// replies ("stub-reply-1", "stub-reply-2", …), so a journey can tell a turn's
// reply from an earlier one the TUI repaints.
const stubReplyPrefix = "stub-reply-"

// stubReply is the stub's number-th main-model text reply.
func stubReply(number int) string {
	return stubReplyPrefix + strconv.Itoa(number)
}

// stubTitleReply answers a small-model request (opencode's session title), so a
// title on screen cannot pass for the turn's reply.
const stubTitleReply = "stub-title"

// ToolCall is a function call the stub answers a turn with: the tool's name and
// its JSON arguments, in the harness's own tool vocabulary.
type ToolCall struct {
	Name      string
	Arguments string
}

// mustJSON encodes value, which the stub builds from its own wire types; it
// never fails for them.
func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		// The stub's wire types always marshal, so this is unreachable.
		return "null"
	}
	return string(data)
}

// assistantRole is the role of every message the stub answers with.
const assistantRole = "assistant"

// stubToolCallID is the id of the stub's one tool call per turn.
const stubToolCallID = "call_stub"

// chatRequest is the part of a chat completion request the stub reads.
type chatRequest struct {
	Model  string            `json:"model"`
	Stream bool              `json:"stream"`
	Tools  []json.RawMessage `json:"tools"`
}

// side reports a side request rather than a turn: one for the small model
// (opencode's and claude-code's titles), or one that offers no tools (codex
// asks its main model for the title without tools). It gets stubTitleReply and
// neither takes the armed tool call nor a numbered reply.
func (r chatRequest) side() bool {
	return r.Model == stubTitleModel || len(r.Tools) == 0
}

// stubCompletionPath is the request path of an OpenAI chat completion.
const stubCompletionPath = "/chat/completions"

// The OpenAI chat completion wire types the stub writes.
type (
	//exhaustruct:ignore
	chatCompletion struct {
		ID      string       `json:"id"`
		Object  string       `json:"object"`
		Created int          `json:"created"`
		Model   string       `json:"model"`
		Choices []chatChoice `json:"choices"`
		Usage   *chatUsage   `json:"usage,omitempty"`
	}
	//exhaustruct:ignore
	chatChoice struct {
		Index        int          `json:"index"`
		Message      *chatMessage `json:"message,omitempty"`
		Delta        *chatMessage `json:"delta,omitempty"`
		FinishReason *string      `json:"finish_reason"`
	}
	//exhaustruct:ignore
	chatMessage struct {
		Role      string         `json:"role,omitempty"`
		Content   *string        `json:"content,omitempty"`
		ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
	}
	//exhaustruct:ignore
	chatToolCall struct {
		Index    *int         `json:"index,omitempty"`
		ID       string       `json:"id"`
		Type     string       `json:"type"`
		Function chatFunction `json:"function"`
	}
	//exhaustruct:ignore
	chatFunction struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	//exhaustruct:ignore
	chatUsage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
)

// serveChatCompletion answers an OpenAI chat completion with call when one is
// given, else with reply; streamed as server-sent chunks when the request asks
// for a stream (opencode's AI SDK provider does) and as one JSON completion
// otherwise.
func serveChatCompletion(response http.ResponseWriter, request chatRequest, call *ToolCall, reply string) {
	message, delta, finishReason := textAnswer(reply)
	if call != nil {
		message, delta, finishReason = toolCallAnswer(*call)
	}
	if request.Stream {
		streamChatCompletion(response, delta, finishReason)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write([]byte(mustJSON(chatCompletion{
		ID: "stub", Object: "chat.completion", Model: stubProviderModel,
		Choices: []chatChoice{{Message: &message, FinishReason: &finishReason}},
		Usage:   &chatUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	})))
}

// textAnswer is a text reply as a completion message, a stream delta, and its
// finish reason.
//
//nolint:nonamedreturns // the three parts are named for the reader
func textAnswer(reply string) (message, delta chatMessage, finishReason string) {
	message = chatMessage{Role: assistantRole, Content: &reply}
	return message, message, "stop"
}

// toolCallAnswer is a tool call as a completion message (no content), a stream
// delta (the call carries its index), and its finish reason.
//
//nolint:nonamedreturns // the three parts are named for the reader
func toolCallAnswer(call ToolCall) (message, delta chatMessage, finishReason string) {
	function := chatFunction(call)
	index := 0
	message = chatMessage{
		Role:      assistantRole,
		ToolCalls: []chatToolCall{{ID: stubToolCallID, Type: "function", Function: function}},
	}
	delta = chatMessage{
		Role:      assistantRole,
		ToolCalls: []chatToolCall{{Index: &index, ID: stubToolCallID, Type: "function", Function: function}},
	}
	return message, delta, "tool_calls"
}

// streamChatCompletion writes delta as one chunk, a finish chunk, and the
// [DONE] sentinel.
func streamChatCompletion(response http.ResponseWriter, delta chatMessage, finishReason string) {
	response.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta chatMessage, finishReason *string) string {
		return mustJSON(chatCompletion{
			ID: "stub", Object: "chat.completion.chunk", Model: stubProviderModel,
			Choices: []chatChoice{{Delta: &delta, FinishReason: finishReason}},
		})
	}
	events := []string{chunk(delta, nil), chunk(chatMessage{}, &finishReason), "[DONE]"}
	for _, event := range events {
		_, _ = fmt.Fprintf(response, "data: %s\n\n", event)
	}
}

// servedTurn reports whether requests include a main-model turn, an OpenAI chat
// completion or Responses request or an Anthropic Messages request (a side
// request or a token count is not one).
func servedTurn(requests []StubRequest) bool {
	for _, request := range requests {
		turnPath := strings.HasSuffix(request.Path, stubCompletionPath) ||
			strings.HasSuffix(request.Path, stubMessagesPath) ||
			strings.HasSuffix(request.Path, stubResponsesPath)
		if request.Method == http.MethodPost && turnPath && !request.Side {
			return true
		}
	}
	return false
}
