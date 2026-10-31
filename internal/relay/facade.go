package relay

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type apiInput struct {
	Prompt string
	Schema any
	Images []apiImage
	Stream bool
}
type apiImage struct {
	Media string
	Data  []byte
}
type apiJob struct {
	ID, Status, Code, Detail, Output, ActualModel string
	Revision                                      uint64
	JSON                                          json.RawMessage
	Error                                         any
	Usage                                         *struct{ InputTokens, OutputTokens, TotalTokens int64 }
}

func (s *Server) facadeRoutes(m *http.ServeMux) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages", "/v1beta/models/{model}"} {
		m.HandleFunc("POST "+path, s.facade)
	}
}
func normalizeAPI(r *http.Request, body map[string]json.RawMessage) (apiInput, error) {
	var in apiInput
	allowed := map[string]bool{"model": true, "stream": true, "messages": true, "input": true, "instructions": true, "system": true, "max_tokens": true, "max_output_tokens": true, "max_completion_tokens": true, "text": true, "response_format": true, "stream_options": true, "contents": true, "systemInstruction": true, "generationConfig": true, "output_config": true, "store": true}
	for key := range body {
		if !allowed[key] {
			return in, fmt.Errorf("unsupported_option_%s", key)
		}
	}
	if raw := body["store"]; len(raw) > 0 {
		var persist bool
		if json.Unmarshal(raw, &persist) != nil || persist {
			return in, errors.New("provider_storage_unsupported")
		}
	}
	var model string
	_ = json.Unmarshal(body["model"], &model)
	if r.PathValue("model") != "" {
		path := r.PathValue("model")
		model = strings.Split(path, ":")[0]
		if !strings.HasSuffix(path, ":generateContent") && !strings.HasSuffix(path, ":streamGenerateContent") {
			return in, errors.New("unsupported_gemini_method")
		}
		in.Stream = strings.HasSuffix(path, ":streamGenerateContent")
	} else {
		if raw := body["stream"]; len(raw) > 0 {
			if json.Unmarshal(raw, &in.Stream) != nil {
				return in, errors.New("invalid_stream")
			}
		}
	}
	if model != "wdc-default" {
		return in, errors.New("use_explicit_wdc-default_model_alias")
	}
	var schema any
	if raw := body["output_config"]; len(raw) > 0 {
		var config struct {
			Format struct {
				Type   string
				Schema any
			}
		}
		if json.Unmarshal(raw, &config) != nil || config.Format.Type != "json_schema" {
			return in, errors.New("unsupported_json_format")
		}
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		if len(fields) != 1 {
			return in, errors.New("unsupported_output_config")
		}
		schema = config.Format.Schema
	}
	if raw := body["text"]; len(raw) > 0 {
		var text struct {
			Format struct {
				Type   string
				Name   string
				Strict bool
				Schema any
			}
		}
		if json.Unmarshal(raw, &text) != nil || text.Format.Type != "json_schema" {
			return in, errors.New("unsupported_json_format")
		}
		schema = text.Format.Schema
	}
	if raw := body["response_format"]; len(raw) > 0 {
		var format struct {
			Type       string
			JSONSchema struct{ Schema any } `json:"json_schema"`
		}
		if json.Unmarshal(raw, &format) != nil || format.Type != "json_schema" {
			return in, errors.New("unsupported_json_format")
		}
		schema = format.JSONSchema.Schema
	}
	if raw := body["generationConfig"]; len(raw) > 0 {
		var config map[string]json.RawMessage
		if json.Unmarshal(raw, &config) != nil {
			return in, errors.New("invalid_generation_config")
		}
		for k := range config {
			if k != "responseMimeType" && k != "responseSchema" && k != "responseJsonSchema" && k != "maxOutputTokens" {
				return in, fmt.Errorf("unsupported_option_%s", k)
			}
		}
		for _, key := range []string{"responseSchema", "responseJsonSchema"} {
			if raw := config[key]; len(raw) > 0 {
				if json.Unmarshal(raw, &schema) != nil {
					return in, errors.New("invalid_json_schema")
				}
			}
		}
		if raw := config["responseMimeType"]; len(raw) > 0 {
			var mime string
			_ = json.Unmarshal(raw, &mime)
			if mime != "application/json" && mime != "text/plain" {
				return in, errors.New("unsupported_response_mime")
			}
			if mime == "application/json" {
				in.Prompt = "Return valid JSON.\n"
			}
		}
		if raw := config["maxOutputTokens"]; len(raw) > 0 {
			body["max_output_tokens"] = raw
		}
	}
	if raw := body["stream_options"]; len(raw) > 0 {
		var options map[string]bool
		if json.Unmarshal(raw, &options) != nil {
			return in, errors.New("invalid_stream_options")
		}
		for key := range options {
			if key != "include_usage" {
				return in, errors.New("unsupported_stream_option")
			}
		}
	}
	var normalizeTypes func(any)
	normalizeTypes = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if typ, ok := v["type"].(string); ok {
				v["type"] = strings.ToLower(typ)
			}
			for _, child := range v {
				normalizeTypes(child)
			}
		case []any:
			for _, child := range v {
				normalizeTypes(child)
			}
		}
	}
	normalizeTypes(schema)
	in.Schema = schema
	contextBody := map[string]any{}
	var walk func(any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case []any:
			for _, item := range v {
				if e := walk(item); e != nil {
					return e
				}
			}
		case map[string]any:
			if role, ok := v["role"].(string); ok && role != "system" && role != "developer" && role != "user" && role != "assistant" && role != "model" {
				return errors.New("unsupported_message_role")
			}
			for _, key := range []string{"tool_calls", "functionCall", "functionResponse", "fileData", "file_data", "audio", "input_audio", "videoMetadata", "cache_control", "citations"} {
				if _, exists := v[key]; exists {
					return fmt.Errorf("unsupported_content_%s", key)
				}
			}
			if typ, _ := v["type"].(string); typ != "" && typ != "text" && typ != "input_text" && typ != "output_text" && typ != "input_image" && typ != "image_url" && typ != "image" && typ != "message" {
				return errors.New("unsupported_content_type")
			}
			if typ, _ := v["type"].(string); typ == "tool_use" || typ == "tool_result" || typ == "function_call" {
				return errors.New("tools_require_relay_sdk")
			}
			imageURL := ""
			media := ""
			data := ""
			if u, ok := v["image_url"].(string); ok {
				imageURL = u
			}
			if u, ok := v["image_url"].(map[string]any); ok {
				imageURL, _ = u["url"].(string)
			}
			if source, ok := v["source"].(map[string]any); ok {
				if source["type"] != "base64" {
					return errors.New("only_inline_images_supported")
				}
				media, _ = source["media_type"].(string)
				data, _ = source["data"].(string)
			}
			if inline, ok := v["inlineData"].(map[string]any); ok {
				media, _ = inline["mimeType"].(string)
				data, _ = inline["data"].(string)
			}
			if imageURL != "" {
				if !strings.HasPrefix(imageURL, "data:") || !strings.Contains(imageURL, ";base64,") {
					return errors.New("remote_images_not_fetched")
				}
				parts := strings.SplitN(imageURL[5:], ";base64,", 2)
				media, data = parts[0], parts[1]
			}
			if data != "" {
				if media != "image/png" && media != "image/jpeg" {
					return errors.New("unsupported_image_format")
				}
				decoded, e := base64.StdEncoding.DecodeString(data)
				if e != nil || len(decoded) > 8<<20 {
					return errors.New("invalid_image")
				}
				in.Images = append(in.Images, apiImage{media, decoded})
				for key := range v {
					delete(v, key)
				}
				v["type"] = "image_attachment"
				v["attachmentIndex"] = len(in.Images)
				return nil
			}
			for _, item := range v {
				if e := walk(item); e != nil {
					return e
				}
			}
		}
		return nil
	}
	hasContent := false
	for _, key := range []string{"input", "messages", "instructions", "system", "contents", "systemInstruction"} {
		if raw := body[key]; len(raw) > 0 {
			var value any
			if json.Unmarshal(raw, &value) != nil {
				return in, errors.New("invalid_content")
			}
			if e := walk(value); e != nil {
				return in, e
			}
			contextBody[key] = value
			if key == "input" || key == "messages" || key == "contents" {
				switch content := value.(type) {
				case string:
					hasContent = hasContent || strings.TrimSpace(content) != ""
				case []any:
					hasContent = hasContent || len(content) > 0
				default:
					hasContent = value != nil
				}
			}
		}
	}
	if !hasContent || len(in.Images) > 7 {
		return in, errors.New("content_required_or_too_many_images")
	}
	b, _ := json.Marshal(contextBody)
	if len(b) > 127<<10 {
		return in, errors.New("context_exceeds_128KiB")
	}
	for _, key := range []string{"max_tokens", "max_output_tokens", "max_completion_tokens"} {
		if raw := body[key]; len(raw) > 0 {
			var budget int
			if json.Unmarshal(raw, &budget) != nil || budget < 1 {
				return in, errors.New("invalid_output_budget")
			}
			contextBody["requested_output_token_budget_hint"] = budget
		}
	}
	in.Prompt += "Follow this application request. Preserve the roles and instruction sections. Return only the requested answer.\n" + string(b)
	return in, nil
}

type rejectedRequest struct{ status int }

func (e *rejectedRequest) Error() string { return fmt.Sprintf("wdc_request_rejected_%d", e.status) }
func callJSON(ctx context.Context, c protocolClient, method, path, lease string, in, out any) error {
	body := ""
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = string(b)
	}
	resp, e := c.call(ctx, method, path, body, lease, "")
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &rejectedRequest{resp.StatusCode}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
	}
	_, e = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	return e
}
func upload(ctx context.Context, c protocolClient, lease, name, media string, data []byte) (string, error) {
	var result struct{ ID string }
	if e := callJSON(ctx, c, "POST", "/v2/inputs", lease, map[string]any{"name": name, "mediaType": media, "size": len(data), "sha256": hash(string(data))}, &result); e != nil {
		return "", e
	}
	for offset := 0; offset < len(data); offset += 24 << 10 {
		end := min(len(data), offset+(24<<10))
		if e := callJSON(ctx, c, "PUT", "/v2/inputs/"+result.ID, lease, map[string]any{"offset": offset, "data": base64.StdEncoding.EncodeToString(data[offset:end])}, nil); e != nil {
			return "", e
		}
	}
	return result.ID, nil
}
func (s *Server) facade(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r)
	if p == nil {
		return
	}
	var body map[string]json.RawMessage
	if !read(w, r, &body, 24<<20) {
		return
	}
	input, e := normalizeAPI(r, body)
	if e != nil {
		problem(w, 400, e.Error())
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		problem(w, 400, "idempotency_key_required")
		return
	}
	connectionID := r.Header.Get("X-WDC-Connection")
	c, e := s.client(p.ID, connectionID, r.Header.Get("X-WDC-Account"))
	if e != nil {
		problem(w, 403, e.Error())
		return
	}
	s.mu.Lock()
	online := time.Since(s.lastSeen[connectionID]) < Grace
	s.mu.Unlock()
	if !online {
		problem(w, 503, "device_offline")
		return
	}
	b, _ := json.Marshal(body)
	if e = s.reserve("facade", hash(connectionID+":"+key), hash(string(b))); e != nil {
		problem(w, 409, "operation_consumed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 600*time.Second)
	defer cancel()
	var lease struct{ Lease string }
	if e = callJSON(ctx, c, "POST", "/v2/lease", "", nil, &lease); e != nil {
		problem(w, 503, "connection_unavailable")
		return
	}
	defer func() {
		cleanup, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		_ = callJSON(cleanup, c, "DELETE", "/v2/lease", lease.Lease, nil, nil)
	}()
	go func() {
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if callJSON(ctx, c, "PUT", "/v2/lease", lease.Lease, nil, nil) != nil {
					cancel()
					return
				}
			}
		}
	}()
	if e = callJSON(ctx, c, "POST", "/v2/groups", lease.Lease, map[string]any{"groups": []map[string]string{{"id": "ai", "name": "AI", "kind": "ai"}}}, nil); e != nil {
		problem(w, 503, "group_unavailable")
		return
	}
	inputs := []string{}
	if len(input.Prompt) > 32768 {
		var id string
		id, e = upload(ctx, c, lease.Lease, "request.txt", "text/plain", []byte(input.Prompt))
		if e != nil {
			problem(w, 400, "context_upload_failed")
			return
		}
		inputs = append(inputs, id)
		input.Prompt = "Follow the attached application request and return only the requested answer."
	}
	for i, image := range input.Images {
		id, err := upload(ctx, c, lease.Lease, fmt.Sprintf("image-%d", i), image.Media, image.Data)
		if err != nil {
			problem(w, 400, "image_upload_failed")
			return
		}
		inputs = append(inputs, id)
	}
	request := map[string]any{"kind": "action", "group": "ai", "prompt": input.Prompt, "inputs": inputs, "idempotencyKey": key, "timeoutSeconds": 600}
	if input.Schema != nil {
		request["outputSchema"] = input.Schema
	}
	var job apiJob
	if e = callJSON(ctx, c, "POST", "/v2/jobs", lease.Lease, request, &job); e != nil {
		var rejected *rejectedRequest
		if errors.As(e, &rejected) && rejected.status < 500 {
			problem(w, rejected.status, "wdc_request_rejected")
			return
		}
		problem(w, 503, "submission_unknown")
		return
	}
	id := "wdc_" + job.ID
	flavor := "chat"
	if strings.HasSuffix(r.URL.Path, "/responses") {
		flavor = "responses"
	} else if strings.HasSuffix(r.URL.Path, "/messages") {
		flavor = "anthropic"
	} else if r.PathValue("model") != "" {
		flavor = "gemini"
	}
	emit := func(event string, v any) bool {
		b, _ := json.Marshal(v)
		if event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
				return false
			}
		}
		_, err := fmt.Fprintf(w, "data: %s\n\n", b)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return err == nil
	}
	sequence := 0
	if input.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		if flavor == "responses" {
			emit("response.created", map[string]any{"type": "response.created", "sequence_number": sequence, "response": responseObject(id, "in_progress", "", nil)})
			sequence++
			emit("response.in_progress", map[string]any{"type": "response.in_progress", "sequence_number": sequence, "response": responseObject(id, "in_progress", "", nil)})
			sequence++
			emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "sequence_number": sequence, "output_index": 0, "item": map[string]any{"id": id + "_message", "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}})
			sequence++
			emit("response.content_part.added", map[string]any{"type": "response.content_part.added", "sequence_number": sequence, "output_index": 0, "item_id": id + "_message", "content_index": 0, "part": outputPart("")})
		}
		if flavor == "chat" {
			emit("", map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "wdc-default", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": ""}, "finish_reason": nil}}})
		}
		if flavor == "anthropic" {
			message := anthropicObject(id, "", nil)
			message["content"] = []any{}
			message["stop_reason"] = nil
			emit("message_start", map[string]any{"type": "message_start", "message": message})
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		}
	}
	output := ""
	for {
		resp, err := c.call(ctx, "GET", "/v2/jobs/"+job.ID+"/events", "", lease.Lease, "")
		if err != nil {
			e = err
			break
		}
		scan := bufio.NewScanner(resp.Body)
		scan.Buffer(make([]byte, 4096), 4<<20)
		for scan.Scan() {
			if !strings.HasPrefix(scan.Text(), "data: ") {
				continue
			}
			if json.Unmarshal([]byte(scan.Text()[6:]), &job) != nil {
				e = errors.New("invalid_job")
				break
			}
			if !strings.HasPrefix(job.Output, output) {
				e = errors.New("output_changed")
				break
			}
			delta := job.Output[len(output):]
			output = job.Output
			if input.Stream && delta != "" {
				sequence++
				switch flavor {
				case "responses":
					emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "sequence_number": sequence, "item_id": id + "_message", "output_index": 0, "content_index": 0, "delta": delta, "logprobs": []any{}})
				case "anthropic":
					emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": delta}})
				case "gemini":
					emit("", geminiObject(delta, nil, false))
				default:
					emit("", map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "wdc-default", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": delta}, "finish_reason": nil}}})
				}
			}
		}
		resp.Body.Close()
		if err = scan.Err(); err != nil {
			e = err
			break
		}
		if job.Status != "running" && job.Status != "queued" && job.Status != "awaiting_approval" {
			break
		}
		if ctx.Err() != nil {
			e = ctx.Err()
			break
		}
	}
	if e != nil || job.Status != "completed" {
		if input.Stream {
			message := "WDC did not confirm completion; do not replay automatically."
			switch flavor {
			case "responses":
				emit("error", map[string]any{"type": "error", "code": "wdc_execution_error", "message": message, "param": nil, "sequence_number": sequence + 1})
			case "gemini":
				emit("", map[string]any{"error": map[string]any{"code": 502, "message": message, "status": "UNAVAILABLE"}})
			default:
				emit("error", map[string]any{"type": "error", "error": map[string]string{"type": "wdc_execution_error", "message": message}})
			}
		} else {
			problem(w, 502, "wdc_execution_not_completed")
		}
		return
	}
	var usage any
	if job.Usage != nil {
		usage = map[string]int64{"input_tokens": job.Usage.InputTokens, "output_tokens": job.Usage.OutputTokens, "total_tokens": job.Usage.TotalTokens}
	}
	if input.Stream {
		switch flavor {
		case "responses":
			sequence++
			emit("response.output_text.done", map[string]any{"type": "response.output_text.done", "sequence_number": sequence, "item_id": id + "_message", "output_index": 0, "content_index": 0, "text": output, "logprobs": []any{}})
			sequence++
			emit("response.content_part.done", map[string]any{"type": "response.content_part.done", "sequence_number": sequence, "item_id": id + "_message", "output_index": 0, "content_index": 0, "part": outputPart(output)})
			sequence++
			emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "sequence_number": sequence, "output_index": 0, "item": outputMessage(id, "completed", output)})
			emit("response.completed", map[string]any{"type": "response.completed", "sequence_number": sequence + 1, "response": responseObject(id, "completed", output, usage)})
		case "anthropic":
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": anthropicUsage(usage)})
			emit("message_stop", map[string]string{"type": "message_stop"})
		case "gemini":
			emit("", geminiObject("", usage, true))
		default:
			emit("", map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "wdc-default", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
		return
	}
	switch flavor {
	case "responses":
		jsonOut(w, 200, responseObject(id, "completed", output, usage))
	case "anthropic":
		jsonOut(w, 200, anthropicObject(id, output, usage))
	case "gemini":
		jsonOut(w, 200, geminiObject(output, usage, true))
	default:
		jsonOut(w, 200, map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": "wdc-default", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": output, "refusal": nil}, "finish_reason": "stop"}}, "usage": chatUsage(usage)})
	}
}
func outputPart(text string) map[string]any {
	return map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}
}
func outputMessage(id, status, text string) map[string]any {
	return map[string]any{"id": id + "_message", "type": "message", "status": status, "role": "assistant", "content": []any{outputPart(text)}}
}
func responseObject(id, status, text string, usage any) map[string]any {
	output := []any{}
	if status == "completed" {
		output = append(output, outputMessage(id, status, text))
	}
	return map[string]any{"id": id, "object": "response", "created_at": time.Now().Unix(), "status": status, "error": nil, "incomplete_details": nil, "model": "wdc-default", "output_text": text, "output": output, "usage": usage}
}
func anthropicUsage(usage any) any {
	if usage == nil {
		return map[string]any{"input_tokens": nil, "output_tokens": nil}
	}
	v := usage.(map[string]int64)
	return map[string]int64{"input_tokens": v["input_tokens"], "output_tokens": v["output_tokens"]}
}
func anthropicObject(id, text string, usage any) map[string]any {
	return map[string]any{"id": id, "type": "message", "role": "assistant", "model": "wdc-default", "content": []any{map[string]string{"type": "text", "text": text}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": anthropicUsage(usage)}
}
func chatUsage(usage any) any {
	if usage == nil {
		return nil
	}
	v := usage.(map[string]int64)
	return map[string]int64{"prompt_tokens": v["input_tokens"], "completion_tokens": v["output_tokens"], "total_tokens": v["total_tokens"]}
}
func geminiObject(text string, usage any, done bool) map[string]any {
	candidate := map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{map[string]string{"text": text}}}}
	if done {
		candidate["finishReason"] = "STOP"
	}
	result := map[string]any{"candidates": []any{candidate}}
	if usage != nil {
		v := usage.(map[string]int64)
		result["usageMetadata"] = map[string]int64{"promptTokenCount": v["input_tokens"], "candidatesTokenCount": v["output_tokens"], "totalTokenCount": v["total_tokens"]}
	}
	return result
}
