package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Anthropic Messages request paths: a turn, and a token count that is not one.
const (
	stubMessagesPath   = "/messages"
	stubCountTokenPath = "/messages/count_tokens"
)

// stubMessageID is the id of the stub's one message per answer.
const stubMessageID = "msg_stub"

// messageType is the type of a whole message (Anthropic) or message item
// (Responses).
const messageType = "message"

// toolUseType is the type of a tool_use content block, and its stop reason.
const toolUseType = "tool_use"

// stubToolUseID is the id of the stub's one tool_use block per turn.
const stubToolUseID = "toolu_stub"

// The Anthropic Messages wire types the stub writes.
type (
	//exhaustruct:ignore
	messagesMessage struct {
		ID           string          `json:"id"`
		Type         string          `json:"type"`
		Role         string          `json:"role"`
		Model        string          `json:"model"`
		Content      []messagesBlock `json:"content"`
		StopReason   *string         `json:"stop_reason"`
		StopSequence *string         `json:"stop_sequence"`
		Usage        messagesUsage   `json:"usage"`
	}
	//exhaustruct:ignore
	messagesBlock struct {
		Type  string          `json:"type"`
		Text  *string         `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	}
	//exhaustruct:ignore
	messagesDelta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
	}
	//exhaustruct:ignore
	messagesUsage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
	//exhaustruct:ignore
	messagesEvent struct {
		Type         string           `json:"type"`
		Message      *messagesMessage `json:"message,omitempty"`
		Index        *int             `json:"index,omitempty"`
		ContentBlock *messagesBlock   `json:"content_block,omitempty"`
		Delta        any              `json:"delta,omitempty"`
		Usage        *messagesUsage   `json:"usage,omitempty"`
	}
	//exhaustruct:ignore
	messagesStop struct {
		StopReason   string  `json:"stop_reason"`
		StopSequence *string `json:"stop_sequence"`
	}
)

// messagesAnswer is one answer in both shapes: the whole content block of a
// message, and the empty start block plus delta of a stream, with the stop
// reason they share.
type messagesAnswer struct {
	block      messagesBlock
	startBlock messagesBlock
	delta      messagesDelta
	stopReason string
}

// textMessage is a text reply.
func textMessage(reply string) messagesAnswer {
	empty := ""
	return messagesAnswer{
		block:      messagesBlock{Type: "text", Text: &reply},
		startBlock: messagesBlock{Type: "text", Text: &empty},
		delta:      messagesDelta{Type: "text_delta", Text: reply},
		stopReason: "end_turn",
	}
}

// toolUseMessage is a tool call; its arguments are the tool input, so they must
// be a JSON object.
func toolUseMessage(call ToolCall) messagesAnswer {
	return messagesAnswer{
		block: messagesBlock{
			Type:  toolUseType,
			ID:    stubToolUseID,
			Name:  call.Name,
			Input: json.RawMessage(call.Arguments),
		},
		startBlock: messagesBlock{Type: toolUseType, ID: stubToolUseID, Name: call.Name, Input: json.RawMessage(`{}`)},
		delta:      messagesDelta{Type: "input_json_delta", PartialJSON: call.Arguments},
		stopReason: toolUseType,
	}
}

// serveMessages answers an Anthropic Messages request with call when one is
// given, else with reply; streamed as server-sent events when the request asks
// for a stream (claude-code does) and as one JSON message otherwise.
func serveMessages(response http.ResponseWriter, request chatRequest, call *ToolCall, reply string) {
	answer := textMessage(reply)
	if call != nil {
		answer = toolUseMessage(*call)
	}
	if request.Stream {
		streamMessages(response, answer)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write([]byte(mustJSON(messagesMessage{
		ID: stubMessageID, Type: messageType, Role: assistantRole, Model: stubProviderModel,
		Content: []messagesBlock{answer.block}, StopReason: &answer.stopReason,
		Usage: messagesUsage{InputTokens: 1, OutputTokens: 1},
	})))
}

// serveTokenCount answers a token count.
func serveTokenCount(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write([]byte(`{"input_tokens":1}`))
}

// streamMessages writes one content block as the Messages event sequence.
func streamMessages(response http.ResponseWriter, answer messagesAnswer) {
	response.Header().Set("Content-Type", "text/event-stream")
	index := 0
	usage := messagesUsage{InputTokens: 1, OutputTokens: 1}
	events := []messagesEvent{
		{Type: "message_start", Message: &messagesMessage{
			ID: stubMessageID, Type: messageType, Role: assistantRole, Model: stubProviderModel,
			Content: []messagesBlock{}, Usage: usage,
		}},
		{Type: "content_block_start", Index: &index, ContentBlock: &answer.startBlock},
		{Type: "content_block_delta", Index: &index, Delta: answer.delta},
		{Type: "content_block_stop", Index: &index},
		{
			Type:  "message_delta",
			Delta: messagesStop{StopReason: answer.stopReason},
			Usage: &messagesUsage{OutputTokens: 1},
		},
		{Type: "message_stop"},
	}
	for _, event := range events {
		_, _ = fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event.Type, mustJSON(event))
	}
}
