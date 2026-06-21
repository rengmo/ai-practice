# AI Practice

从 0 开始在一个项目里迭代练习 AI 能力；每个知识点对应 `docs/` 下的一篇文档。

## 当前能力

| 能力 | 说明 | 文档 |
|------|------|------|
| SSE 流式对话 | 打字机效果、Markdown、token / 费用统计 | [docs/01_chat_sse.md](docs/01_chat_sse.md) |
| Function Calling | 获取时间 / 季节、生成时令菜谱 PPT | [docs/02_function_call.md](docs/02_function_call.md) |

## 项目结构

```
ai-practice/
├── server.go          # 程序入口（go run server.go）
├── internal/app/      # 业务逻辑：Agent、工具、PPT、HTTP
├── index.html         # 聊天界面
├── output/            # 生成的 PPT（运行时自动创建）
└── docs/              # 各知识点说明
```

## 运行

```bash
export DEEPSEEK_API_KEY="你的key"
export DEEPSEEK_MODEL="deepseek-v4-pro"   # 可选
go run server.go   # 或 go run .
```

浏览器打开 <http://127.0.0.1:8000>。

## 试试这些问题

- `现在几点了？` → 调用 `get_current_time`
- `当前是什么季节？` → 调用 `get_current_season`
- `把当前季节的合适菜谱整理为 PPT 发给我` → 先查季节，再生成 PPT，页面出现下载链接
