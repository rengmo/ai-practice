package app

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type chatRequest struct {
	Messages []json.RawMessage `json:"messages"`
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, seq *int, payload any) {
	*seq++
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "id: %d\n", *seq)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
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

	messages, err := parseMessages(req.Messages)
	if err != nil {
		http.Error(w, "bad messages", http.StatusBadRequest)
		return
	}
	messages = ensureSystemPrompt(messages)

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	seq := 0
	working, total, err := runAgent(messages, func(payload any) {
		writeSSE(w, flusher, &seq, payload)
	})
	if err != nil {
		writeSSE(w, flusher, &seq, map[string]string{"type": "error", "message": err.Error()})
		return
	}

	writeSSE(w, flusher, &seq, map[string]any{
		"type": "usage",
		"usage": map[string]any{
			"model":             model,
			"prompt_tokens":     total.PromptTokens,
			"completion_tokens": total.CompletionTokens,
			"total_tokens":      total.TotalTokens,
			"cache_hit_tokens":  total.CacheHitTokens,
			"cache_miss_tokens": total.CacheMissTokens,
		},
	})
	writeSSE(w, flusher, &seq, map[string]any{
		"type":     "done",
		"messages": messagesForClient(working),
	})
}

func ensureSystemPrompt(messages []Message) []Message {
	const prompt = "你是一个专业的美食助手。可以调用工具获取当前时间、当前季节；当用户要把时令菜谱整理成 PPT 时，先调用 get_current_season 确认季节，构思 3～6 道菜后调用 generate_recipe_ppt 生成文件，并在最终回复里告知下载链接。不要编造时间或假装已生成文件。"
	if len(messages) > 0 && messages[0].Role == "system" {
		messages[0].Content = prompt
		return messages
	}
	return append([]Message{{Role: "system", Content: prompt}}, messages...)
}

func handleDownloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/downloads/")
	name, err := url.PathUnescape(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(outputDir, name)
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(name))
	http.ServeFile(w, r, path)
}

func contentDispositionAttachment(filename string) string {
	ascii := filename
	if !isASCII(filename) {
		ascii = "download.pptx"
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(filename))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Run 启动 HTTP 服务。
func Run() {
	if apiKey == "" {
		log.Fatal("请先设置环境变量 DEEPSEEK_API_KEY")
	}
	if err := ensureOutputDir(); err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/downloads/", handleDownloads)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			http.ServeFile(w, r, "index.html")
			return
		}
		http.NotFound(w, r)
	})

	addr := "127.0.0.1:8000"
	log.Printf("打开浏览器访问：http://%s  （Ctrl+C 退出）", addr)
	log.Printf("已启用工具: get_current_time / get_current_season / generate_recipe_ppt")
	log.Fatal(http.ListenAndServe(addr, nil))
}
