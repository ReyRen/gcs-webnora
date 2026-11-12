package types

import (
	"context"
	"testing"
)

func TestModelBindingsRemainResourceScoped(t *testing.T) {
	caller := Caller{UserID: "collaborator", TenantID: 20, Role: TenantRoleViewer}
	ctx := WithCaller(context.Background(), caller)
	ctx = WithModelBindings(ctx, "owner-embedding", "owner-chat")
	if !HasModelBinding(ctx, "owner-embedding") || HasModelBinding(ctx, "unrelated-owner-model") {
		t.Fatal("binding grant must contain only the configured models")
	}
	ctx = WithExecutionTenant(ctx, 10)
	if CallerFromContext(ctx) != caller {
		t.Fatal("model execution must preserve the authenticated caller")
	}
	ctx = WithModelBindings(ctx, "other-resource-chat")
	if HasModelBinding(ctx, "owner-embedding") || !HasModelBinding(ctx, "other-resource-chat") {
		t.Fatal("a new resource must replace the previous binding scope")
	}
	if HasModelBinding(context.Background(), "owner-embedding") {
		t.Fatal("unbound requests must not receive resource execution rights")
	}
}
