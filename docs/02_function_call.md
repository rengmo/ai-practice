# Function Calling · 从「知道」到「做到」

在 [01_chat_sse](01_chat_sse.md) 里，模型只会生成文字。本章在同一个项目里叠加 **Function Calling**：模型输出 `tool_call`，Go 代码真正执行，结果塞回模型后再回答用户。

## 项目结构（当前）

```
ai-practice/
├── server.go              # 程序入口：go run server.go
├── index.html             # 聊天 UI + SSE 消费 + PPT 下载
├── internal/app/
│   ├── server.go          # HTTP：/api/chat、/downloads/
│   ├── agent.go           # Agent 循环、调 DeepSeek
│   ├── tools.go           # 工具注册 + runTool 执行
│   └── pptx.go            # 标准库 zip 生成 .pptx
└── output/                # 生成的 PPT（运行时创建）
```

## 内置工具

| 工具 | 作用 | 典型触发语 |
|------|------|-----------|
| `get_current_time` | 返回服务器当前时间（上海时区） | 「现在几点了？」 |
| `get_current_season` | 返回当前季节 + 饮食建议 | 「现在什么季节？」 |
| `generate_recipe_ppt` | 把菜谱列表写成 `.pptx` 并提供下载 | 「把时令菜谱做成 PPT 发我」 |

## 一次完整流程（生成 PPT）

用户：「把当前季节的合适菜谱整理为 PPT 发给我」

```
用户提问
   │
   ▼
第 1 轮 LLM → tool_call: get_current_season()
   │              宿主 runTool → "当前是夏季（6-8月，宜清热解暑）"
   ▼
第 2 轮 LLM → tool_call: generate_recipe_ppt({ title, season, recipes })
   │              宿主 runTool → 写入 output/夏季菜谱_xxx.pptx
   │              SSE 推送 file 事件 → 前端显示下载链接
   ▼
第 3 轮 LLM → 无 tool_calls，直接文字回答（附下载说明）
```

共 **3 轮** `chatOnce`（3 次 LLM 请求），**2 次**工具执行，任务完成后**提前退出**，不会跑满 8 轮。

## Agent 循环（核心代码）

`internal/app/agent.go` 中的 `runAgent`：

```go
func runAgent(messages []Message, onEvent SSEEventHandler) ([]Message, usageTotals, error) {
    working := append([]Message(nil), messages...)
    var total usageTotals

    for step := 1; step <= 8; step++ {                    // 上限 8 轮
        reply, u, err := chatOnce(working)                // 非流式，才能解析 tool_calls
        // ... 累加 token ...

        working = append(working, reply)

        if len(reply.ToolCalls) == 0 {                    // 模型不再调工具 → 正常结束
            if reply.Content != "" {
                onEvent(map[string]string{"type": "delta", "content": reply.Content})
            }
            return working, total, nil
        }

        for _, call := range reply.ToolCalls {
            onEvent(map[string]any{"type": "tool_start", "name": fn, "args": args})
            result, _ := runTool(call.Function.Name, call.Function.Arguments)
            onEvent(map[string]any{"type": "tool_done", "name": fn, "result": result.Text})
            if result.FileURL != "" {
                onEvent(map[string]any{"type": "file", "url": result.FileURL, "filename": result.FileName})
            }
            working = append(working, Message{Role: "tool", ToolCallID: call.ID, Content: result.Text})
        }
    }
    return working, total, fmt.Errorf("超过最大工具调用步数（8）")
}
```

`onEvent` 是回调：`server.go` 传入 `writeSSE`，把 `tool_start` / `tool_done` / `file` / `delta` 推给浏览器。

工具轮次用**非流式**请求（才能拿到完整 `tool_calls`）；最终文字通过 SSE `delta` 一次性推给前端。

---

## 为什么是 8 次？多步任务怎么办？

### 8 是什么

**8 不是「任务一定要 8 步」**，而是 **「最多允许 8 轮 LLM 往返」的安全上限**（保险丝）。

每一轮 = 一次 `chatOnce` = 问 DeepSeek 一次。模型可以在任意一轮说「我答完了」（`ToolCalls` 为空），循环**提前结束**。

| 任务 | 实际轮数 | 是否跑满 8 |
|------|---------|-----------|
| 问时间 | 2 轮（调工具 + 回答） | 否 |
| 生成 PPT | 3 轮（季节 + PPT + 回答） | 否 |
| 复杂多工具链 | 可能 5～7 轮 | 仍可能否 |

### 为什么不无限循环

- 模型可能**反复调同一工具**，永远停不下来
- 每一轮都花钱（API token）
- 用户请求会**长时间挂起**

所以工程上必须设 `maxSteps`，LangChain / Cursor Agent 等框架也一样。

### 多步任务怎么处理

**推荐做法（按优先级）：**

1. **加大上限（最简单）**  
   把 `8` 改成常量，例如 `const maxAgentSteps = 16`，或从环境变量 `AGENT_MAX_STEPS` 读取。复杂任务调到 15～25 即可。

2. **不要问 LLM「要循环几次」**  
   步数应由**宿主程序**控制，不应让模型决定「我还需要 5 轮」。模型只负责每一步「调不调工具、调哪个」；何时停 = `ToolCalls 为空` 或 `step > maxSteps`。

3. **同一轮可并行多个 tool_call**  
   当前代码在一轮里 `for _, call := range reply.ToolCalls` 可执行多个工具，再进入下一轮。少占轮数。

4. **进阶（以后可加）**  
   - 检测重复调用同一工具 + 相同参数 → 强制终止  
   - 超步数时让 LLM 用已有结果做**降级总结**  
   - 人工确认后再继续（危险操作）

### 小结

```
正常路径：  模型说「完成了」→ 提前 return（大多数任务 2～4 轮）
异常路径：  8 轮还在调工具 → 报错，防止死循环 + 控费
扩展方式：  调大 maxSteps 常量 / 环境变量，而不是无限 loop 或问 LLM 要次数
```

---

## 前端看到什么

聊天区除了 AI 气泡，还会出现：

- 灰色条：`⚙️ 调用工具 get_current_season…`
- 灰色条：`✓ get_current_season → 当前是夏季…`
- 蓝色卡片：`📎 PPT 已生成：[文件名]`（点击 fetch 下载，支持中文文件名）

`done` 事件带回精简后的 `messages`（去掉中间 `tool` 消息），供 `localStorage` 持久化。

PPT 下载走 `/downloads/文件名.pptx`；后端设置正确 `Content-Type` 与 UTF-8 文件名。

## 和纯 Chat 的对比

| | 纯 Chat（01） | Function Calling（本章） |
|--|--------------|-------------------------|
| 模型输出 | 只有文字 | 可能是 `tool_calls` JSON |
| 当前时间 | 模型会猜 / 拒答 | `get_current_time` 返回真实值 |
| PPT | 模型只能描述怎么做 | `generate_recipe_ppt` 真的生成文件 |
| 幻觉 | 容易编造事实 | 关键数据来自工具执行结果 |

## 练习建议

1. 先问「现在几点」，在 DevTools → Network → EventStream 看 `tool_start` / `tool_done`
2. 再问「把当前季节菜谱做成 PPT」，观察是否 3 轮结束
3. 打开 `output/` 用 WPS / Keynote 查看生成的 `.pptx`
4. 在 `tools.go` 加一个 `get_weather` 工具，体会注册 + `runTool` 分支
5. 把 `agent.go` 里的 `8` 抽成 `maxAgentSteps` 常量，试着改成 16

官方文档：[DeepSeek Tool Calls](https://api-docs.deepseek.com/guides/tool_calls)
