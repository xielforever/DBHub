package auth

import "context"

type claimsCtxKey struct{}

// ClaimsContextKey 认证用户 Claims 在 request context 中的键。
var ClaimsContextKey claimsCtxKey

// ClaimsFromContext 从 context 取出认证声明。
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ClaimsContextKey).(*Claims)
	return c, ok
}
