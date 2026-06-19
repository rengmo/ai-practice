# 最简网页版对话（SSE 流式 + 每轮 token / 费用统计）

一个**零框架**的对话示例：后端只用 Go 标准库，前端只有一个纯 HTML/JS 文件。
跑起来你能看到：

- 像 ChatGPT 一样的「打字机效果」（回答一块一块蹦出来）；
- AI 回复支持 **Markdown 渲染**（`marked` 解析 + `DOMPurify` 消毒，防 XSS）；
- 每轮对话下面显示**这一轮消耗了多少 token**，以及 DeepSeek 的**缓存命中 / 未命中**；
- 每轮和整个会话的**人民币费用估算**（按 DeepSeek 官方价目表计算）；
- 底部黄条显示整个会话的累计 token 与累计费用；
- 刷新页面后对话历史不丢失（存于 `localStorage`）。

## 项目结构

```
ai-practice/
├── server.go    # Go 后端：静态页面 + /api/chat SSE 接口
├── index.html   # 纯前端：聊天界面、SSE 消费、Markdown、token / 费用统计
├── go.mod       # Go 模块（无第三方依赖，仅用标准库）
└── README.md
```

## 怎么跑

```bash
cd ai-practice

export DEEPSEEK_API_KEY="你的key"
export DEEPSEEK_MODEL="deepseek-v4-pro"   # 可选，默认 deepseek-v4-pro；也可换 deepseek-v4-flash
go run server.go
```

然后浏览器打开 <http://127.0.0.1:8000>，开始聊天。

> 未设置 `DEEPSEEK_API_KEY` 时服务不会启动。

**环境要求：** Go 1.27+；前端 Markdown 库通过 CDN 加载（需联网）。

## 这两个文件各干嘛

| 文件 | 角色 |
|------|------|
| `index.html` | 纯前端：聊天界面 + 用 `fetch` 读取 SSE 流 + Markdown 渲染 + 统计 token / 费用 + `localStorage` 持久化。无任何框架。 |
| `server.go` | 纯后端：标准库 `net/http`，发网页 + 提供 `/api/chat` 的 SSE 接口。无 gin/echo。 |
| `go.mod` | 模块名 `chat_sse`，无外部依赖。 |

> 为什么要有后端？因为 **API Key 不能放在前端**（写在网页里等于公开泄露），而且浏览器直连第三方 API 通常有跨域限制。所以由这个本地后端「代收请求、藏好 Key、把流式结果转发给浏览器」。

---

## SSE 是什么、怎么用

**SSE（Server-Sent Events，服务器推送事件）** = 浏览器发一个**普通 HTTP 请求**，服务器**不立刻关闭连接**，而是持续不断地往浏览器**单向推送**数据，直到推完为止。

它的报文格式极其简单——每条消息就是一行 `data:`，以一个**空行**（`\n\n`）结尾：

```
id: 1
data: {"type":"delta","content":"你"}

id: 2
data: {"type":"delta","content":"好"}

id: 3
data: {"type":"usage","usage":{"model":"deepseek-v4-pro","prompt_tokens":12,"completion_tokens":8,"total_tokens":20,"cache_hit_tokens":10,"cache_miss_tokens":2}}

id: 4
data: {"type":"done"}

```

### 后端怎么发（`server.go` 关键三步）

```go
// 1) 用「事件流」这个 Content-Type 告诉浏览器：这是持续推送
w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")

// 2) 每生成一小块，就按 id: <序号>\ndata: <json>\n\n 的格式写一条
fmt.Fprintf(w, "id: %d\n", seq)
fmt.Fprintf(w, "data: %s\n\n", jsonBytes)

// 3) 立刻 Flush，把这一块真的发出去，别攒在缓冲区里
flusher.Flush()
```

模型每吐一个 token，我们就推一条 `delta`；全部结束后再推一条 `usage`（这一轮的 token 用量、模型名、缓存命中/未命中），最后推一条 `done`。

### 前端怎么收（`index.html` 关键）

因为我们要用 **POST** 把多轮对话历史发给后端，所以用 `fetch` + 读 `body` 流的方式来收 SSE（浏览器内置的 `EventSource` 只支持 GET，发不了请求体）：

```js
const resp = await fetch("/api/chat", { method: "POST", body: JSON.stringify({ messages }) });
const reader = resp.body.getReader();
const decoder = new TextDecoder();
let buf = "";
while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  buf += decoder.decode(value, { stream: true });
  let sep;
  while ((sep = buf.indexOf("\n\n")) >= 0) {        // 按空行切出一条条消息
    const line = buf.slice(0, sep).trim();
    buf = buf.slice(sep + 2);
    if (!line.startsWith("data:")) continue;
    const evt = JSON.parse(line.slice(5).trim());
    // evt.type 为 delta / usage / done，分别处理
  }
}
```

> 小贴士：如果你的接口是 GET 且不需要发请求体，直接用浏览器内置的 `EventSource` 更省事，它自带断线自动重连：
> ```js
> const es = new EventSource("/api/chat?q=你好");
> es.onmessage = (e) => console.log(JSON.parse(e.data));
> ```

---

## 为什么用 SSE，而不是 WebSocket？

对话这个场景的数据流是**单向**的：模型生成的文字需要**持续地从服务器推给浏览器**，而浏览器这一侧，每轮只在用户点「发送」时发一次请求即可，**不需要在同一条连接里频繁地往服务器回传**。SSE 正是为这种「服务器单向推流」而生。

| 对比项 | SSE | WebSocket |
|------|------|-----------|
| 方向 | **服务器 → 浏览器**（单向推送） | 双向、全双工 |
| 底层协议 | 就是普通 HTTP，`Content-Type: text/event-stream` | 需要从 HTTP「升级（Upgrade）」成 ws 协议，单独握手 |
| 实现复杂度 | **极低**：后端按 `data:\n\n` 写、前端读流即可 | 较高：要管帧、心跳、连接状态 |
| 断线重连 | 浏览器 `EventSource` **自带**重连 | 需自己实现重连逻辑 |
| 经过代理/防火墙 | 友好（就是普通 HTTP 响应） | 有时会被中间设备拦截 |
| 适合场景 | LLM 流式回答、进度推送、通知 | 在线游戏、协同编辑、实时聊天室等**双向高频**交互 |

一句话：**LLM 流式输出是「服务器单向吐字」，SSE 刚好够用且最简单；WebSocket 是为「双向实时」准备的，用在这里属于杀鸡用牛刀**——更重、要额外握手与心跳、还得自己处理重连。所以 OpenAI、DeepSeek 等的流式接口走的都是 SSE。

## 它和「token 计费 / 缓存命中 / 人民币费用」的关系

- 每轮统计来自接口返回的 `usage`：`prompt_tokens`（输入）、`completion_tokens`（输出）、`total_tokens`（合计）。
- DeepSeek 还额外给出 `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`，对应文档里讲的**缓存命中 / 未命中**。
- 后端在 `usage` 里附带 `model` 字段，前端据此选择对应价目表。

### 费用怎么算

**单位：元 / 百万 token**：

```
本轮费用 ≈ 命中缓存的输入 token × 命中单价
        + 未命中缓存的输入 token × 未命中单价
        + 输出 token × 输出单价
```

| 计费项 | deepseek-v4-flash | deepseek-v4-pro |
|------|:---:|:---:|
| 输入（缓存命中） | 0.02 元 | 0.025 元 |
| 输入（缓存未命中） | 1 元 | 3 元 |
| 输出 | 2 元 | 6 元 |

前端收到 `usage` 后按上表估算人民币，显示在：

- **每轮统计行**：`本轮：… token · 费用 ¥0.000123`
- **底部黄条**：`本会话累计：… token · 费用 ¥0.001234`

> 这是按官方价目表的**估算值**，方便直观感受成本；实际账单以 DeepSeek 控制台为准。若接口未返回缓存命中/未命中字段，则输入 token 全部按「未命中」计价（偏保守）。

### 多轮对话为什么越聊越省

- 多聊几轮你会观察到：因为每轮都把**完整历史**发过去、且前缀不变，**前面的历史会命中缓存**，所以越往后「命中」的 token 越多、费用越低。这正是把「固定内容放前面」能省钱的直观体现。
