// demo 10：最简单的网页版对话（SSE 流式 + 每轮 token 统计），Go 版。
//
// 「零框架」示例：
//   - 后端：只用 Go 标准库 net/http（没有 gin/echo）。
//   - 前端：只有一个纯 HTML/JS 文件 index.html（没有 React/Vue）。
//
// 后端做两件事：
//  1. 把 index.html 这个静态页面发给浏览器。
//  2. 提供 /api/chat 接口，用 SSE（text/event-stream）把模型回答「一块一块」推给浏览器，
//     最后再推一条 usage，告诉前端这一轮花了多少 token、命中了多少缓存。
//
// 为什么要有这个后端？因为 API Key 不能写在前端（会泄露），且浏览器直连第三方 API 有跨域限制。
// 所以让这个本地后端代收请求、藏好 Key、转发流式结果给浏览器。
//
// 运行：
//
//	cd practice/llm_demos/10_chat_sse
//	export DEEPSEEK_API_KEY="你的key"   # 必填，没有就不启动
//	go run server.go
//
// 然后浏览器打开 http://127.0.0.1:8000
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
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

// 浏览器发来的请求体：{"messages": [{"role": "...", "content": "..."}]}
type chatRequest struct {
	Messages []json.RawMessage `json:"messages"`
}

// 转发给 DeepSeek 的请求体。
type upstreamRequest struct {
	Model         string            `json:"model"`
	Messages      []json.RawMessage `json:"messages"`
	Stream        bool              `json:"stream"`
	StreamOptions map[string]bool   `json:"stream_options"`
}

// DeepSeek 流式返回的每个 chunk（只取我们关心的字段）。
type upstreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int  `json:"prompt_tokens"`
		CompletionTokens int  `json:"completion_tokens"`
		TotalTokens      int  `json:"total_tokens"`
		CacheHitTokens   *int `json:"prompt_cache_hit_tokens"`
		CacheMissTokens  *int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
}

// 把一个对象打包成一条 SSE 消息并立刻发出。
// SSE 标准格式：
//
//	id: <序号>
//	data: <json>
//
//	（空行结束本条消息）
func writeSSE(w http.ResponseWriter, flusher http.Flusher, seq *int, payload any) {
	*seq++
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "id: %d\n", *seq)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush() // 关键：立刻把这一块发出去，别攒着
}

// logJSON 把任意内容格式化成带缩进的 JSON 打印出来，方便在终端观察。
// tag 是这条日志的标签（比如函数名）。无法序列化时退回普通打印。
func logJSON(tag string, v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("【%s】%+v", tag, v)
		return
	}
	log.Printf("【%s】\n%s", tag, b)
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// SSE 响应头：告诉浏览器这是一条「持续推送」的事件流
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	seq := 0
	if err := streamFromDeepSeek(w, flusher, &seq, req.Messages); err != nil {
		writeSSE(w, flusher, &seq, map[string]string{"type": "error", "message": err.Error()})
		return
	}
	writeSSE(w, flusher, &seq, map[string]string{"type": "done"})
}

// 调用真实 DeepSeek，逐块推送文本；最后推一次 usage（token 统计）。
func streamFromDeepSeek(w http.ResponseWriter, flusher http.Flusher, seq *int, messages []json.RawMessage) error {
	logJSON("streamFromDeepSeek messages", messages)
	body, _ := json.Marshal(upstreamRequest{
		Model:         model,
		Messages:      messages,
		Stream:        true,
		StreamOptions: map[string]bool{"include_usage": true}, // 要在流式结尾拿到 token 用量，必须开启
	})

	httpReq, err := http.NewRequest(http.MethodPost, deepseekURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("DeepSeek 返回 %d：%s", resp.StatusCode, msg)
	}

	// DeepSeek 也用 SSE 返回：一行行的  data: {...}，以  data: [DONE]  结束
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}

		var chunk upstreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			if delta := chunk.Choices[0].Delta.Content; delta != "" {
				writeSSE(w, flusher, seq, map[string]string{"type": "delta", "content": delta})
			}
		}
		if u := chunk.Usage; u != nil {
			writeSSE(w, flusher, seq, map[string]any{
				"type": "usage",
				"usage": map[string]any{
					"model":               model,
					"prompt_tokens":       u.PromptTokens,
					"completion_tokens":   u.CompletionTokens,
					"total_tokens":        u.TotalTokens,
					"cache_hit_tokens":    u.CacheHitTokens,  // DeepSeek 特有：命中缓存的输入 token 数
					"cache_miss_tokens":   u.CacheMissTokens, // 未命中缓存的输入 token 数
				},
			})
		}
	}
	return scanner.Err()
}

func main() {
	if apiKey == "" {
		log.Fatal("请先设置环境变量 DEEPSEEK_API_KEY")
	}

	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			http.ServeFile(w, r, "index.html")
			return
		}
		http.NotFound(w, r)
	})

	addr := "127.0.0.1:8000" 
	log.Printf("打开浏览器访问：http://%s  （Ctrl+C 退出）", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
