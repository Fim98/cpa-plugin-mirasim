package mirasim

import (
	"context"
	"strings"
)

type RelayOptions struct {
	Collect *bool
	Locale  string
}

type requestIdentityKey struct{}
type requestIdentity struct{ session, turn string }

// Values come from CPA execution metadata, never untrusted x-mirasim-* headers.
func WithRequestIdentity(ctx context.Context, metadata map[string]any) context.Context {
	session, _ := metadata["execution_session_id"].(string)
	turn, _ := metadata["mirasim_turn_id"].(string)
	return context.WithValue(ctx, requestIdentityKey{}, requestIdentity{safeMetadata(session), safeMetadata(turn)})
}

func safeMetadata(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}
