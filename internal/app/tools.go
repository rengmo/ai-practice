package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const outputDir = "output"

type ToolResult struct {
	Text     string
	FileURL  string
	FileName string
}

var toolDefs = []Tool{
	{
		Type: "function",
		Function: toolFunction{
			Name:        "get_current_time",
			Description: "获取当前日期、时间和星期。用户问「现在几点」「今天几号」时必须调用，不要编造。",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	},
	{
		Type: "function",
		Function: toolFunction{
			Name:        "get_current_season",
			Description: "获取当前季节（按北半球公历：春3-5月、夏6-8月、秋9-11月、冬12-2月）。整理时令菜谱前应先调用。",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	},
	{
		Type: "function",
		Function: toolFunction{
			Name:        "generate_recipe_ppt",
			Description: "把时令菜谱整理成 PPT 文件并提供下载。用户要求「菜谱做成 PPT」「发我 PPT」时，先想好菜谱内容再调用。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":  map[string]any{"type": "string", "description": "PPT 标题，如「春季养生菜谱推荐」"},
					"season": map[string]any{"type": "string", "description": "季节名称，如春季、夏季"},
					"recipes": map[string]any{
						"type":        "array",
						"description": "菜谱列表，建议 3～6 道",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":        map[string]any{"type": "string", "description": "菜名"},
								"ingredients": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "主要食材"},
								"steps":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "简要步骤"},
								"tip":         map[string]any{"type": "string", "description": "时令小贴士（可选）"},
							},
							"required": []string{"name", "ingredients", "steps"},
						},
					},
				},
				"required": []string{"title", "season", "recipes"},
			},
		},
	},
}

type recipeInput struct {
	Name        string   `json:"name"`
	Ingredients []string `json:"ingredients"`
	Steps       []string `json:"steps"`
	Tip         string   `json:"tip"`
}

type pptArgs struct {
	Title   string        `json:"title"`
	Season  string        `json:"season"`
	Recipes []recipeInput `json:"recipes"`
}

func ensureOutputDir() error {
	return os.MkdirAll(outputDir, 0o755)
}

func runTool(name, argsJSON string) (ToolResult, error) {
	switch name {
	case "get_current_time":
		return ToolResult{Text: currentTimeText()}, nil
	case "get_current_season":
		return ToolResult{Text: currentSeasonText()}, nil
	case "generate_recipe_ppt":
		var args pptArgs
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return ToolResult{}, fmt.Errorf("参数解析失败: %w", err)
		}
		if args.Title == "" || len(args.Recipes) == 0 {
			return ToolResult{}, fmt.Errorf("需要 title 和至少一道 recipes")
		}
		if err := ensureOutputDir(); err != nil {
			return ToolResult{}, err
		}
		safeName := sanitizeFilename(args.Season + "菜谱")
		if safeName == "" {
			safeName = "recipe"
		}
		filename := fmt.Sprintf("%s_%s.pptx", safeName, time.Now().Format("20060102_150405"))
		path := filepath.Join(outputDir, filename)
		if err := writeRecipePPTX(path, args.Title, args.Season, args.Recipes); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{
			Text:     fmt.Sprintf("已生成 PPT：%s（共 %d 页，含封面）", filename, len(args.Recipes)+1),
			FileURL:  "/downloads/" + filename,
			FileName: filename,
		}, nil
	default:
		return ToolResult{}, fmt.Errorf("未知工具: %s", name)
	}
}

func currentTimeText() string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	now := time.Now().In(loc)
	weekdays := []string{"日", "一", "二", "三", "四", "五", "六"}
	return fmt.Sprintf("%s 星期%s", now.Format("2006年1月2日 15:04:05"), weekdays[now.Weekday()])
}

func currentSeasonText() string {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	month := int(time.Now().In(loc).Month())
	season, desc := seasonOf(month)
	return fmt.Sprintf("当前是%s（%s）", season, desc)
}

func seasonOf(month int) (season, desc string) {
	switch {
	case month >= 3 && month <= 5:
		return "春季", "3-5月，宜清淡养肝"
	case month >= 6 && month <= 8:
		return "夏季", "6-8月，宜清热解暑"
	case month >= 9 && month <= 11:
		return "秋季", "9-11月，宜润燥养肺"
	default:
		return "冬季", "12-2月，宜温补御寒"
	}
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 0x4e00 && r <= 0x9fff:
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}
