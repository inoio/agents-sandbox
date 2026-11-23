package journeys

import (
	"fmt"
	"net/http"
)

// stubResponsesPath is the request path of an OpenAI Responses turn.
const stubResponsesPath = "/responses"

// completedStatus marks a finished Responses item or response.
const completedStatus = "completed"

// stubFunctionCallID is the call id of the stub's one function call per turn.
const stubFunctionCallID = "call_stub"

// The OpenAI Responses wire types the stub writes.
type (
	//exhaustruct:ignore
	responsesEvent struct {
		Type         string           `json:"type"`
		Response     *responsesStatus `json:"response,omitempty"`
		OutputIndex  *int             `json:"output_index,omitempty"`
		ContentIndex *int             `json:"content_index,omitempty"`
		Delta        string           `json:"delta,omitempty"`
		Item         *responsesItem   `json:"item,omitempty"`
	}
	//exhaustruct:ignore
	responsesStatus struct {
		ID     string          `json:"id"`
		Status string          `json:"status"`
		Usage  *responsesUsage `json:"usage,omitempty"`
	}
	//exhaustruct:ignore
	responsesUsage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	}
	//exhaustruct:ignore
	responsesItem struct {
		Type      string             `json:"type"`
		ID        string             `json:"id"`
		Status    string             `json:"status"`
		Role      string             `json:"role,omitempty"`
		Content   []responsesContent `json:"content,omitempty"`
		CallID    string             `json:"call_id,omitempty"`
		Name      string             `json:"name,omitempty"`
		Arguments string             `json:"arguments,omitempty"`
	}
	//exhaustruct:ignore
	responsesContent struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Annotations []any  `json:"annotations"`
	}
)

// serveResponses answers an OpenAI Responses request (codex) as the event
// stream codex reads: response.created, the reply's text delta (for a text
// reply), the finished output item, and response.completed. A function call is
// one function_call item with the call's JSON arguments.
func serveResponses(response http.ResponseWriter, call *ToolCall, reply string) {
	zero := 0
	item := responsesItem{
		Type: messageType, ID: stubMessageID, Status: completedStatus, Role: assistantRole,
		Content: []responsesContent{{Type: "output_text", Text: reply, Annotations: []any{}}},
	}
	if call != nil {
		item = responsesItem{
			Type: "function_call", ID: "fc_stub", Status: completedStatus,
			CallID: stubFunctionCallID, Name: call.Name, Arguments: call.Arguments,
		}
	}
	events := []responsesEvent{
		{Type: "response.created", Response: &responsesStatus{ID: "resp_stub", Status: "in_progress"}},
	}
	if call == nil {
		events = append(events, responsesEvent{
			Type: "response.output_text.delta", OutputIndex: &zero, ContentIndex: &zero, Delta: reply,
		})
	}
	events = append(events,
		responsesEvent{Type: "response.output_item.done", OutputIndex: &zero, Item: &item},
		responsesEvent{Type: "response.completed", Response: &responsesStatus{
			ID:     "resp_stub",
			Status: completedStatus,
			Usage:  &responsesUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		}},
	)
	response.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		_, _ = fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event.Type, mustJSON(event))
	}
}
