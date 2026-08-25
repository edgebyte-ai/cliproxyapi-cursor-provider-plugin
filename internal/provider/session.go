package provider

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/edgebyte-ai/cliproxyapi-cursor-native-plugin/internal/pb"
	"google.golang.org/protobuf/proto"
)

const (
	connectFlagCompressed = 0x01
	connectFlagEndStream  = 0x02
)

var errTurnFinished = errors.New("Cursor turn finished")
var toolCallIDUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

type TurnInput struct {
	RootMessages [][]byte
	UserText     string
	Tools        []*pb.McpToolDefinition
}

type TurnEvent struct {
	Type       string
	Text       string
	ToolCallID string
	ToolName   string
	ToolArgs   map[string]any
	Tokens     int
	DoneReason string
	Err        error
}

func (s *Service) runTurn(ctx context.Context, storage AuthStorage, model string, input TurnInput) (<-chan TurnEvent, error) {
	cfg := s.Config()
	ctx, cancel := contextWithTimeout(ctx, cfg.RequestTimeout())
	pipeReader, pipeWriter := io.Pipe()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.CursorBaseURL, "/")+"/agent.v1.AgentService/Run", pipeReader)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("build Cursor stream request: %w", err)
	}
	request.Header.Set("Content-Type", "application/connect+proto")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("TE", "trailers")
	request.Header.Set("Authorization", "Bearer "+storage.AccessToken)
	request.Header.Set("X-Ghost-Mode", "true")
	request.Header.Set("X-Cursor-Client-Version", cfg.ClientVersion)
	request.Header.Set("X-Cursor-Client-Type", "cli")
	requestID, _ := randomUUID()
	request.Header.Set("X-Request-Id", requestID)
	if len(cfg.AllowedNativeTools) > 0 {
		request.Header.Set("X-Cursor-Agent-Allowed-Tools", strings.Join(cfg.AllowedNativeTools, ","))
	}

	responseCh := make(chan struct {
		response *http.Response
		err      error
	}, 1)
	go func() {
		response, doErr := newH2Client(cfg.RequestTimeout()).Do(request)
		responseCh <- struct {
			response *http.Response
			err      error
		}{response: response, err: doErr}
	}()

	blobs := make(map[string][]byte, len(input.RootMessages))
	rootIDs := make([][]byte, 0, len(input.RootMessages))
	for _, message := range input.RootMessages {
		id := sha256.Sum256(message)
		blobs[fmt.Sprintf("%x", id[:])] = append([]byte(nil), message...)
		rootIDs = append(rootIDs, append([]byte(nil), id[:]...))
	}
	conversationID, _ := randomUUID()
	action := &pb.ConversationAction{}
	if strings.TrimSpace(input.UserText) != "" {
		messageID, _ := randomUUID()
		action.Action = &pb.ConversationAction_UserMessageAction{UserMessageAction: &pb.UserMessageAction{UserMessage: &pb.UserMessage{Text: input.UserText, MessageId: messageID}}}
	} else {
		action.Action = &pb.ConversationAction_ResumeAction{ResumeAction: &pb.ResumeAction{}}
	}
	runRequest := &pb.AgentRunRequest{
		ConversationState: &pb.ConversationStateStructure{RootPromptMessagesJson: rootIDs},
		Action:            action,
		ModelDetails:      &pb.ModelDetails{ModelId: model, DisplayModelId: model, DisplayName: model},
		RequestedModel:    &pb.RequestedModel{ModelId: model},
		ConversationId:    &conversationID,
	}
	if len(input.Tools) > 0 {
		runRequest.McpTools = &pb.McpTools{McpTools: input.Tools}
	}

	var writeMu sync.Mutex
	send := func(message *pb.AgentClientMessage) error {
		payload, marshalErr := proto.Marshal(message)
		if marshalErr != nil {
			return marshalErr
		}
		frame := make([]byte, 5+len(payload))
		binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
		copy(frame[5:], payload)
		writeMu.Lock()
		defer writeMu.Unlock()
		_, writeErr := pipeWriter.Write(frame)
		return writeErr
	}
	if err := send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_RunRequest{RunRequest: runRequest}}); err != nil {
		cancel()
		_ = pipeWriter.CloseWithError(err)
		return nil, fmt.Errorf("start Cursor turn: %w", err)
	}

	result := <-responseCh
	if result.err != nil {
		cancel()
		_ = pipeWriter.CloseWithError(result.err)
		return nil, fmt.Errorf("open Cursor turn: %w", result.err)
	}
	if result.response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(result.response.Body, maxJSONResponseBytes))
		_ = result.response.Body.Close()
		cancel()
		_ = pipeWriter.Close()
		return nil, cursorStatusError(raw, result.response.StatusCode)
	}

	events := make(chan TurnEvent, 32)
	go func() {
		defer close(events)
		defer cancel()
		defer result.response.Body.Close()
		defer pipeWriter.Close()
		reader := bufio.NewReader(result.response.Body)
		for {
			flag, payload, frameErr := readConnectFrame(reader)
			if frameErr != nil {
				if frameErr != io.EOF && ctx.Err() == nil {
					events <- TurnEvent{Type: "done", DoneReason: "error", Err: frameErr}
				}
				return
			}
			if flag&connectFlagCompressed != 0 {
				payload, frameErr = gunzipPayload(payload)
				if frameErr != nil {
					events <- TurnEvent{Type: "done", DoneReason: "error", Err: frameErr}
					return
				}
			}
			if flag&connectFlagEndStream != 0 {
				if len(bytes.TrimSpace(payload)) > 0 && !bytes.Equal(bytes.TrimSpace(payload), []byte("{}")) {
					events <- TurnEvent{Type: "done", DoneReason: "error", Err: cursorStatusError(payload, http.StatusBadGateway)}
				} else {
					events <- TurnEvent{Type: "done", DoneReason: "stop"}
				}
				return
			}
			var message pb.AgentServerMessage
			if err := proto.Unmarshal(payload, &message); err != nil {
				continue
			}
			if handleErr := handleServerMessage(&message, blobs, input.Tools, send, events); handleErr != nil {
				if errors.Is(handleErr, errTurnFinished) {
					return
				}
				events <- TurnEvent{Type: "done", DoneReason: "error", Err: handleErr}
				return
			}
			if update := message.GetInteractionUpdate(); update != nil && update.GetTurnEnded() != nil {
				events <- TurnEvent{Type: "done", DoneReason: "stop"}
				return
			}
		}
	}()
	return events, nil
}

func readConnectFrame(reader *bufio.Reader) (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > 64<<20 {
		return 0, nil, fmt.Errorf("Cursor frame exceeds 64 MiB")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

func gunzipPayload(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, 64<<20))
}

func handleServerMessage(message *pb.AgentServerMessage, blobs map[string][]byte, tools []*pb.McpToolDefinition, send func(*pb.AgentClientMessage) error, events chan<- TurnEvent) error {
	if kv := message.GetKvServerMessage(); kv != nil {
		if args := kv.GetGetBlobArgs(); args != nil {
			data := blobs[fmt.Sprintf("%x", args.GetBlobId())]
			client := &pb.KvClientMessage{Id: kv.GetId(), Message: &pb.KvClientMessage_GetBlobResult{GetBlobResult: &pb.GetBlobResult{BlobData: data}}}
			return send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_KvClientMessage{KvClientMessage: client}})
		}
		if args := kv.GetSetBlobArgs(); args != nil {
			blobs[fmt.Sprintf("%x", args.GetBlobId())] = append([]byte(nil), args.GetBlobData()...)
			client := &pb.KvClientMessage{Id: kv.GetId(), Message: &pb.KvClientMessage_SetBlobResult{SetBlobResult: &pb.SetBlobResult{}}}
			return send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_KvClientMessage{KvClientMessage: client}})
		}
	}
	if execMessage := message.GetExecServerMessage(); execMessage != nil {
		if execMessage.GetRequestContextArgs() != nil {
			result := &pb.RequestContextResult{Result: &pb.RequestContextResult_Success{Success: &pb.RequestContextSuccess{RequestContext: &pb.RequestContext{
				Tools: tools,
				Env:   &pb.RequestContextEnv{OsVersion: "linux", Shell: "bash", SandboxEnabled: false, TimeZone: "UTC"},
			}}}}
			client := &pb.ExecClientMessage{Id: execMessage.GetId(), ExecId: execMessage.GetExecId(), Message: &pb.ExecClientMessage_RequestContextResult{RequestContextResult: result}}
			return send(&pb.AgentClientMessage{Message: &pb.AgentClientMessage_ExecClientMessage{ExecClientMessage: client}})
		}
		if args := execMessage.GetMcpArgs(); args != nil {
			decoded := make(map[string]any, len(args.GetArgs()))
			for key, value := range args.GetArgs() {
				decoded[key] = decodeValue(value)
			}
			events <- TurnEvent{Type: "tool_call", ToolCallID: normalizeToolCallID(args.GetToolCallId()), ToolName: firstNonEmpty(args.GetName(), args.GetToolName(), "unknown"), ToolArgs: decoded}
			events <- TurnEvent{Type: "done", DoneReason: "tool_calls"}
			return errTurnFinished
		}
	}
	if update := message.GetInteractionUpdate(); update != nil {
		if delta := update.GetTextDelta(); delta != nil && delta.GetText() != "" {
			events <- TurnEvent{Type: "text", Text: delta.GetText()}
		}
		if delta := update.GetThinkingDelta(); delta != nil && delta.GetText() != "" {
			events <- TurnEvent{Type: "thinking", Text: delta.GetText()}
		}
		if delta := update.GetTokenDelta(); delta != nil {
			events <- TurnEvent{Type: "usage", Tokens: int(delta.GetTokens())}
		}
	}
	return nil
}

func normalizeToolCallID(value string) string {
	value = strings.Trim(toolCallIDUnsafe.ReplaceAllString(strings.TrimSpace(value), "_"), "_")
	if value == "" {
		return "call_" + mustUUID()
	}
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func decodeValue(raw []byte) any {
	var value pb.Value
	if err := proto.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	switch kind := value.Kind.(type) {
	case *pb.Value_StringValue:
		return kind.StringValue
	case *pb.Value_NumberValue:
		return kind.NumberValue
	case *pb.Value_BoolValue:
		return kind.BoolValue
	case *pb.Value_NullValue:
		return nil
	case *pb.Value_StructValue:
		out := make(map[string]any, len(kind.StructValue.GetFields()))
		for key, inner := range kind.StructValue.GetFields() {
			encoded, _ := proto.Marshal(inner)
			out[key] = decodeValue(encoded)
		}
		return out
	case *pb.Value_ListValue:
		out := make([]any, 0, len(kind.ListValue.GetValues()))
		for _, inner := range kind.ListValue.GetValues() {
			encoded, _ := proto.Marshal(inner)
			out = append(out, decodeValue(encoded))
		}
		return out
	default:
		return nil
	}
}

func mustUUID() string {
	id, _ := randomUUID()
	return id
}

func unaryProto(ctx context.Context, path string, body []byte, token string, cfg Config, timeout time.Duration) ([]byte, http.Header, error) {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/proto")
	headers.Set("Connect-Protocol-Version", "1")
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("X-Ghost-Mode", "true")
	headers.Set("X-Cursor-Client-Version", cfg.ClientVersion)
	headers.Set("X-Cursor-Client-Type", "cli")
	requestID, _ := randomUUID()
	headers.Set("X-Request-Id", requestID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.CursorBaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header = headers
	resp, err := newH2Client(timeout).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, resp.Header, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.Header, cursorStatusError(raw, resp.StatusCode)
	}
	return raw, resp.Header, nil
}

func jsonBytes(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
