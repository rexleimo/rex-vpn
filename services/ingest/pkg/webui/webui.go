// Package webui 是 ingest admin 页与 user-center 共享的 SSR 组件层（work-7）。
//
// 技术: stdlib html/template + go:embed（原决策 htmlx 不可获取, 按 §0 风险预案回退;
// 渲染层隔离在本包, 换模板引擎只改这里）。无 Node 构建链, 单二进制。
// 组件: Layout / Nav / Card / DataTable / Badge / Spinner + 轻量 CSS(Mantine 风变量)。
package webui

import (
	"embed"
	"html/template"
	"io"
	"net/http"
	"strings"
)

//go:embed base.html app.css
var assets embed.FS

// CSS 返回组件库样式(页面 <head> 内联)。
func CSS() string {
	b, err := assets.ReadFile("app.css")
	if err != nil {
		return ""
	}
	return string(b)
}

// baseSrc 返回基础布局与组件 define 块。
func baseSrc() string {
	b, err := assets.ReadFile("base.html")
	if err != nil {
		return ""
	}
	return string(b)
}

// BaseTemplate 解析基础布局与组件 define, 可追加页面级 define 片段。
func BaseTemplate(extra ...string) (*template.Template, error) {
	src := baseSrc()
	for _, e := range extra {
		src += "\n" + e
	}
	return template.New("webui").Funcs(template.FuncMap{
		"join": func(parts []string, sep string) string {
			return strings.Join(parts, sep)
		},
		"substr": func(s string, start, length int) string {
			if start >= len(s) {
				return ""
			}
			end := start + length
			if end > len(s) {
				end = len(s)
			}
			return s[start:end]
		},
		// dict 构造 map(键值成对), 供 {{template "x" (dict "K" .V ...)}} 传参。
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				if k, ok := kv[i].(string); ok {
					m[k] = kv[i+1]
				}
			}
			return m
		},
	}).Parse(src)
}

// Render 执行模板命名块 name 到 w。
func Render(t *template.Template, w io.Writer, name string, data any) error {
	return t.ExecuteTemplate(w, name, data)
}

// WritePage 完整渲染一个管理页: Content-Type + 执行命名块。
func WritePage(w http.ResponseWriter, t *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = Render(t, w, name, data)
}
