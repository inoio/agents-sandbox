package journeys

import (
	"fmt"
	"net/http"
)

// Anthropic Messages request paths: a turn, and a token count that is not one.
const (
	stubMessagesPath   = "/messages"
	stubCountTokenPath = "/messages/count_tokens"
)

// stubToolUseID is the id of the stub's one tool_use block per turn.
const stubToolUseID = "toolu_stub"

// messagesAnswer is one answer in both shapes: the whole content block of a
// message, and the empty start block plus delta of a stream, with the stop
// reason they share.
type messagesAnswer struct {
	block      string
	startBlock string
	delta      string
	stopReason string
}

// textMessage is a text reply.
func textMessage(reply string) messagesAnswer {
	return messagesAnswer{
		block:      fmt.Sprintf(`{"type":"text","text":%s}`, jsonString(reply)),
		startBlock: `{"type":"text","text":""}`,
		delta:      fmt.Sprintf(`{"type":"text_delta","text":%s}`, jsonString(reply)),
		stopReason: "end_turn",
	}
}

// toolUseMessage is a tool call.
func toolUseMessage(call ToolCall) messagesAnswer {
	return messagesAnswer{
		block: fmt.Sprintf(
			`{"type":"tool_use","id":%q,"name":%s,"input":%s}`,
			stubToolUseID,
			jsonString(call.Name),
			call.Arguments,
		),
		startBlock: fmt.Sprintf(
			`{"type":"tool_use","id":%q,"name":%s,"input":{}}`,
			stubToolUseID,
			jsonString(call.Name),
		),
		delta:      fmt.Sprintf(`{"type":"input_json_delta","partial_json":%s}`, jsonString(call.Arguments)),
		stopReason: "tool_use",
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
	_, _ = fmt.Fprintf(response,
		`{"id":"msg_stub","type":"message","role":"assistant","model":%q,"content":[%s],`+
			`"stop_reason":%q,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`,
		stubProviderModel, answer.block, answer.stopReason)
}

// serveTokenCount answers a token count.
func serveTokenCount(response http.ResponseWriter) {
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write([]byte(`{"input_tokens":1}`))
}

// streamMessages writes one content block as the Messages event sequence.
func streamMessages(response http.ResponseWriter, answer messagesAnswer) {
	response.Header().Set("Content-Type", "text/event-stream")
	events := []struct{ name, data string }{
		{"message_start", fmt.Sprintf(
			`{"type":"message_start","message":{"id":"msg_stub","type":"message","role":"assistant","model":%q,`+
				`"content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
			stubProviderModel)},
		{
			"content_block_start",
			fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":%s}`, answer.startBlock),
		},
		{"content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":%s}`, answer.delta)},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", fmt.Sprintf(
			`{"type":"message_delta","delta":{"stop_reason":%s,"stop_sequence":null},"usage":{"output_tokens":1}}`,
			jsonString(answer.stopReason))},
		{"message_stop", `{"type":"message_stop"}`},
	}
	for _, event := range events {
		_, _ = fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event.name, event.data)
	}
}
