package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMCPServer(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	t.Chdir(dir)
	e, err := newEnv("1.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	e.Server, e.Dir, e.Interactive = os.Getenv("MKTAUTO_SERVER"), dir, false
	c, _ := e.client()
	call := func(level int, lines ...string) []map[string]any {
		e.mcpLevel = level
		var tools []mcpTool
		for _, tl := range e.mcpTools(c) {
			if tl.level <= level {
				tools = append(tools, tl)
			}
		}
		var out strings.Builder
		if err := e.serveMCP(context.Background(), strings.NewReader(strings.Join(lines, "\n")), &out, tools); err != nil {
			t.Fatal(err)
		}
		var res []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("not JSON on stdout: %q", l)
			}
			res = append(res, m)
		}
		return res
	}
	text := func(m map[string]any) (string, bool) {
		r := m["result"].(map[string]any)
		isErr, _ := r["isError"].(bool)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), isErr
	}
	res := call(1,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_post","arguments":{"project":"Memoshot","accounts":["facebook"],"caption":"From an assistant","hashtags":["ai"]}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"create_post","arguments":{"project":"Memoshot","accounts":["facebook"],"caption":"x","when":"now"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"publish_post","arguments":{"post":"last"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_post","arguments":{"post":"last"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"nope"}`,
	)
	if len(res) != 8 { // the notification gets no answer
		t.Fatalf("answers: %d %v", len(res), res)
	}
	if info := res[0]["result"].(map[string]any); info["protocolVersion"] != "2025-06-18" || info["serverInfo"].(map[string]any)["name"] != "mktauto" {
		t.Errorf("initialize: %v", info)
	}
	var names []string
	for _, tl := range res[1]["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if !contains(names, "create_post") || contains(names, "publish_post") || contains(names, "schedule_post") {
		t.Errorf("drafts level tools: %v", names)
	}
	if s, isErr := text(res[2]); isErr || !strings.Contains(s, "Memoshot") {
		t.Errorf("list_projects: %s", s)
	}
	if s, isErr := text(res[3]); isErr || len(f.posts) != 1 || f.posts[0]["baseCaption"] != "From an assistant" {
		t.Errorf("create_post: %v %s", isErr, s)
	}
	if s, isErr := text(res[4]); !isErr || !strings.Contains(s, "--allow publish") {
		t.Errorf("publishing needs --allow publish: %s", s)
	}
	if res[5]["error"] == nil {
		t.Errorf("publish_post isn't offered at drafts level: %v", res[5])
	}
	if s, isErr := text(res[6]); isErr || !strings.Contains(s, `"id"`) {
		t.Errorf("get_post: %s", s)
	}
	if res[7]["error"].(map[string]any)["code"] != float64(-32601) {
		t.Errorf("unknown method: %v", res[7])
	}
	// Publish level: publish_post works.
	res = call(3, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"publish_post","arguments":{"post":"last"}}}`)
	if s, isErr := text(res[0]); isErr || len(f.published) != 1 {
		t.Errorf("publish: %v %s", isErr, s)
	}
}
