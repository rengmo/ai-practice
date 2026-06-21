package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const deepseekURL = "https://api.deepseek.com/chat/completions"

var (
	apiKey = os.Getenv("DEEPSEEK_API_KEY")
	model  = envOr("DEEPSEEK_MODEL", "deepseek-v4-pro")
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type apiChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools"`
}

type apiChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int  `json:"prompt_tokens"`
		CompletionTokens int  `json:"completion_tokens"`
		TotalTokens      int  `json:"total_tokens"`
		CacheHitTokens   *int `json:"prompt_cache_hit_tokens"`
		CacheMissTokens  *int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
}

type usageTotals struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CacheHitTokens   int
	CacheMissTokens  int
}

type SSEEventHandler func(payload any)

func chatOnce(messages []Message) (Message, *usageTotals, error) {
	body, _ := json.Marshal(apiChatRequest{Model: model, Messages: messages, Tools: toolDefs})

	req, err := http.NewRequest(http.MethodPost, deepseekURL, bytes.NewReader(body))
	if err != nil {
		return Message{}, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Message{}, nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return Message{}, nil, fmt.Errorf("DeepSeek 返回 %d：%s", resp.StatusCode, raw)
	}

	var cr apiChatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return Message{}, nil, err
	}
	if len(cr.Choices) == 0 {
		return Message{}, nil, fmt.Errorf("API 无 choices")
	}

	var u usageTotals
	if cr.Usage != nil {
		u.PromptTokens = cr.Usage.PromptTokens
		u.CompletionTokens = cr.Usage.CompletionTokens
		u.TotalTokens = cr.Usage.TotalTokens
		if cr.Usage.CacheHitTokens != nil {
			u.CacheHitTokens = *cr.Usage.CacheHitTokens
		}
		if cr.Usage.CacheMissTokens != nil {
			u.CacheMissTokens = *cr.Usage.CacheMissTokens
		}
	}
	return cr.Choices[0].Message, &u, nil
}

func runAgent(messages []Message, onEvent SSEEventHandler) ([]Message, usageTotals, error) {
	working := append([]Message(nil), messages...)
	var total usageTotals

	for step := 1; step <= 8; step++ {
		reply, u, err := chatOnce(working)
		if err != nil {
			return working, total, err
		}
		total.PromptTokens += u.PromptTokens
		total.CompletionTokens += u.CompletionTokens
		total.TotalTokens += u.TotalTokens
		total.CacheHitTokens += u.CacheHitTokens
		total.CacheMissTokens += u.CacheMissTokens

		working = append(working, reply)

		if len(reply.ToolCalls) == 0 {
			if reply.Content != "" {
				onEvent(map[string]string{"type": "delta", "content": reply.Content})
			}
			return working, total, nil
		}

		for _, call := range reply.ToolCalls {
			fn := call.Function.Name
			args := call.Function.Arguments
			onEvent(map[string]any{"type": "tool_start", "name": fn, "args": args})

			result, err := runTool(fn, args)
			if err != nil {
				result = ToolResult{Text: "错误: " + err.Error()}
			}
			onEvent(map[string]any{
				"type":   "tool_done",
				"name":   fn,
				"result": result.Text,
			})
			if result.FileURL != "" {
				onEvent(map[string]any{
					"type":     "file",
					"url":      result.FileURL,
					"filename": result.FileName,
				})
			}

			working = append(working, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result.Text,
			})
		}
	}

	return working, total, fmt.Errorf("超过最大工具调用步数（8）")
}

func parseMessages(raw []json.RawMessage) ([]Message, error) {
	out := make([]Message, 0, len(raw))
	for _, r := range raw {
		var m Message
		if err := json.Unmarshal(r, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func messagesForClient(working []Message) []Message {
	out := make([]Message, 0, len(working))
	for _, m := range working {
		switch m.Role {
		case "system", "user":
			out = append(out, m)
		case "assistant":
			if m.Content != "" {
				out = append(out, Message{Role: "assistant", Content: m.Content})
			}
		}
	}
	return out
}
