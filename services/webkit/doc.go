// Package webkit 是仓库内 Go SSR 服务(ingest admin / user-center)共享的
// gin Web 套件(ADR-0001)：会话、登录限流、安全头、渲染 helper。
//
// 原则：只放"两个以上服务会用"的通用件；业务鉴权语义(谁可看什么)留在各服务。
// 替代此前两服务各自手撸的 session.go / auth() / rate 限流。
package webkit
