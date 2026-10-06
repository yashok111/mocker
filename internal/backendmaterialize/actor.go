package backendmaterialize

import "context"

type actorKey struct{}
type Actor struct {
	Name, Source string
	OwnerID      *int64
}

func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}
func actorFrom(ctx context.Context) Actor {
	if actor, ok := ctx.Value(actorKey{}).(Actor); ok {
		return actor
	}
	return Actor{Name: "system", Source: "mcp"}
}
