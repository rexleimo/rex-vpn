// Package webui 是各 Go SSR 服务(ingest admin / user-center)共享的组件层。
//
// 技术: stdlib html/template + go:embed。原决策 htmlx 不可获取, 按 ingest §0 风险预案回退;
// 渲染层隔离在本包, 换模板引擎只改这里。无 Node 构建链。
//
// 共享内容: 轻量 CSS(Mantine 风变量) + 组件 define(Badge/Spinner) + 模板函数(dict/substr/join)。
// 各服务自带 layout(导航/壳)与页面 define, 经 ComponentsTemplate 并入同一 template set。
package webui

import (
	"embed"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"
)

//go:embed components.html app.css theme.css wm-ui.js dist/bootstrap.min.css dist/bootstrap.bundle.min.js
var assets embed.FS

// CSS 返回组件库样式(页面 <head> 内联)。旧 token 层, admin 未迁移前保留(ADR-0002 后果)。
func CSS() string {
	b, err := assets.ReadFile("app.css")
	if err != nil {
		return ""
	}
	return string(b)
}

// ThemeCSS 返回 Bootstrap 之上的 Apple 风主题层(小体积, 页面内联)。
// 返回 template.CSS: 嵌入内容受信, 跳过 html/template 在 <style> 上下文的净化(否则输出 ZgotmplZ)。
func ThemeCSS() template.CSS {
	b, err := assets.ReadFile("theme.css")
	if err != nil {
		return ""
	}
	return template.CSS(b)
}

// Asset 返回 vendored 静态资产(bootstrap css/js)内容与 MIME。
func Asset(name string) ([]byte, string, bool) {
	switch name {
	case "wm-ui.js":
		b, err := assets.ReadFile(name)
		if err != nil {
			return nil, "", false
		}
		return b, "application/javascript; charset=utf-8", true
	case "bootstrap.min.css", "bootstrap.bundle.min.js":
		b, err := assets.ReadFile("dist/" + name)
		if err != nil {
			return nil, "", false
		}
			if strings.HasSuffix(name, ".js") {
			return b, "application/javascript; charset=utf-8", true
		}
		return b, "text/css; charset=utf-8", true
	}
	return nil, "", false
}

// AssetHandler 供各服务挂 /assets/<name>(gin 兼容 http.HandlerFunc)。
func AssetHandler(name string) http.HandlerFunc {
	body, mime, ok := Asset(name)
	return func(w http.ResponseWriter, r *http.Request) {
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		_, _ = w.Write(body)
	}
}

func componentsSrc() string {
	b, err := assets.ReadFile("components.html")
	if err != nil {
		return ""
	}
	return string(b)
}

// ComponentsTemplate 解析共享组件 define + 各服务追加的 layout/页面 define 片段。
// 返回的 template set 里可直接执行任意命名块(layout 或 page)。
func ComponentsTemplate(extra ...string) (*template.Template, error) {
	src := componentsSrc()
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
		"div64": func(a, b int64) int64 { // 整除(向下取整), 供模板做单位换算
			if b == 0 {
				return 0
			}
			return a / b
		},
		"timefmt": func(unix int64, layout string) string {
			if unix <= 0 {
				return ""
			}
			return time.Unix(unix, 0).Format(layout)
		},
	}).Parse(src)
}

// Render 执行模板命名块 name 到 w。
func Render(t *template.Template, w io.Writer, name string, data any) error {
	return t.ExecuteTemplate(w, name, data)
}

// WritePage 完整渲染: Content-Type + 执行命名块(通常是该服务的 layout)。
func WritePage(w http.ResponseWriter, t *template.Template, layoutName string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = Render(t, w, layoutName, data)
}
