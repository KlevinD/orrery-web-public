package identity

import "context"

type principalContextKey struct{}
type trustedInternalServiceContextKey struct{}

func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, NormalizePrincipal(principal))
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok {
		return Principal{}, false
	}
	principal = NormalizePrincipal(principal)
	return principal, principal.Authenticated()
}

func ContextWithTrustedInternalService(ctx context.Context) context.Context {
	return context.WithValue(ctx, trustedInternalServiceContextKey{}, true)
}

func TrustedInternalServiceFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	trusted, _ := ctx.Value(trustedInternalServiceContextKey{}).(bool)
	return trusted
}
