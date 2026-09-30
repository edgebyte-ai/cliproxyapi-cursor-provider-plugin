package provider

import (
	"errors"
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
	"google.golang.org/protobuf/proto"
)

func TestMCPDiscoveryReturnsCallerCatalog(t *testing.T) {
	for _, tools := range [][]*pb.McpToolDefinition{nil, {{Name: "get_weather", ToolName: "get_weather", ProviderIdentifier: "openai-caller"}}} {
		message := &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{
			Id: 7, ExecId: "discovery",
			Message: &pb.ExecServerMessage_McpStateExecArgs{McpStateExecArgs: &pb.McpStateExecArgs{ServerIdentifiers: []string{"openai-caller"}}},
		}}}
		var response *pb.AgentClientMessage
		err := handleServerMessage(message, nil, tools, func(m *pb.AgentClientMessage) error { response = m; return nil }, make(chan TurnEvent, 2))
		if err != nil || response == nil {
			t.Fatalf("discovery failed: response=%v err=%v", response, err)
		}
		exec := response.GetExecClientMessage()
		if exec.GetId() != 7 || exec.GetExecId() != "discovery" {
			t.Fatalf("response IDs do not match: %v", exec)
		}
		result := exec.GetMcpStateExecResult().GetSuccess()
		if result == nil {
			t.Fatal("discovery did not return success")
		}
		if len(tools) == 0 {
			if len(result.GetServers()) != 0 {
				t.Fatal("empty catalog advertised a server")
			}
			continue
		}
		servers := result.GetServers()
		if len(servers) != 1 || servers[0].GetServerIdentifier() != "openai-caller" || len(servers[0].GetTools()) != 1 || !proto.Equal(servers[0].GetTools()[0], tools[0]) {
			t.Fatalf("discovery omitted caller tool: %v", servers)
		}
	}
}

func TestMCPCallPreservesCallerNameAfterDiscovery(t *testing.T) {
	message := &pb.AgentServerMessage{Message: &pb.AgentServerMessage_ExecServerMessage{ExecServerMessage: &pb.ExecServerMessage{
		Message: &pb.ExecServerMessage_McpArgs{McpArgs: &pb.McpArgs{Name: "openai-caller-get_weather", ToolName: "get_weather", ToolCallId: "weather-call"}},
	}}}
	events := make(chan TurnEvent, 2)
	err := handleServerMessage(message, nil, nil, nil, events)
	if !errors.Is(err, errTurnFinished) {
		t.Fatalf("error = %v", err)
	}
	call := <-events
	if call.Type != "tool_call" || call.ToolName != "get_weather" || call.ToolCallID != "weather-call" {
		t.Fatalf("unexpected caller tool call: %+v", call)
	}
	if done := <-events; done.Type != "done" || done.DoneReason != "tool_calls" {
		t.Fatalf("unexpected completion: %+v", done)
	}
}
