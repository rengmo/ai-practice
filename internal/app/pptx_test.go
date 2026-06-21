package app

import "testing"

func TestWriteRecipePPTX(t *testing.T) {
	if err := ensureOutputDir(); err != nil {
		t.Fatal(err)
	}
	recipes := []recipeInput{{
		Name:        "凉拌黄瓜",
		Ingredients: []string{"黄瓜", "蒜", "醋"},
		Steps:       []string{"黄瓜拍碎", "加调料拌匀"},
		Tip:         "夏季开胃",
	}}
	if err := writeRecipePPTX("output/_test.pptx", "夏季菜谱", "夏季", recipes); err != nil {
		t.Fatal(err)
	}
}
