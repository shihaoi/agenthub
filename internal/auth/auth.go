package auth

import (
	"context"
	"net/http"
	"strings"

	"agenthub/internal/db"
)

// contextKey 是用于在 context.Context 中存储和检索值的自定义类型。
// 使用自定义类型可以避免不同包在 context 中存储数据时使用的键名发生冲突。
type contextKey string

// agentContextKey 是专门用于在 context 中存储经过身份验证的 Agent 信息的键。
const agentContextKey contextKey = "agent"

// AgentFromContext 提取存储在请求上下文 (context) 中的 Agent 对象。
// 它主要在中间件验证通过后，被路由的 handler 函数调用来获取当前发请求的 Agent 的信息。
func AgentFromContext(ctx context.Context) *db.Agent {
	a, _ := ctx.Value(agentContextKey).(*db.Agent)
	return a
}

// Middleware 创建一个 HTTP 中间件，用于验证请求的 Bearer 令牌是否属于有效 Agent。
// 它通过数据库校验传入的 API Key，成功则将对应的 Agent 信息存入 context 供后续 handler 使用。
func Middleware(database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 从 HTTP 请求头中提取 Authorization 字段的 Bearer 令牌
			key := extractBearer(r)
			if key == "" {
				// 如果 token 为空，返回 401 未授权错误
				http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
				return
			}
			
			// 根据 API Key 从数据库中查询对应的 Agent 记录
			agent, err := database.GetAgentByAPIKey(key)
			if err != nil {
				// 数据库查询出错时，返回 500 内部服务器错误
				http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
				return
			}
			if agent == nil {
				// 找不到对应的 Agent，表示 API Key 无效，返回 401 未授权错误
				http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
				return
			}
			
			// 验证成功，将 Agent 对象注入到当前请求的 context 中
			ctx := context.WithValue(r.Context(), agentContextKey, agent)
			// 使用带有 Agent 信息的上下文继续处理请求
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// AdminMiddleware 创建一个 HTTP 中间件，用于验证请求的 Bearer 令牌是否与服务器配置的管理员密钥匹配。
// 只有匹配成功，请求才会被允许进入管理(Admin)相关的接口 handler。
func AdminMiddleware(adminKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 从 HTTP 请求头中提取 Authorization 字段的 Bearer 令牌
			key := extractBearer(r)
			// 如果 token 为空或者与配置的管理员密钥不匹配
			if key == "" || key != adminKey {
				// 返回 401 未授权错误
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			// 验证成功，继续处理后续的 HTTP 请求
			next.ServeHTTP(w, r)
		})
	}
}

// extractBearer 是一个辅助函数，用于从 Request 的 Header (Authorization) 中提取 Bearer 令牌的值。
// 它会检查该字段是否以 "Bearer " 为前缀，并返回前缀之后的实际 token 字符串。
func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return h[7:] // 返回 "Bearer " （7个字符）之后的部分
	}
	return ""
}
