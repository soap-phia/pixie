package transport

import (
	"context"
	"io"
)

type Transport interface {
	io.Closer

	ReadMessage(ctx context.Context) ([]byte, error)

	WriteMessage(ctx context.Context, data []byte) error
}
