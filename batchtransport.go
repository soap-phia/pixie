package pixie

import (
	"context"
	"sync"
)

type WriteRequest struct {
	data []byte
	ctx  context.Context
}

type BatchTransport struct {
	Transport    Transport
	WriteMutex   sync.Mutex
	WriteChannel chan WriteRequest
	CloseOnce    sync.Once
	CloseChannel chan struct{}
}

func NewBatchTransport(t Transport, batchSize int) *BatchTransport {
	if batchSize <= 0 {
		batchSize = 128
	}

	bt := &BatchTransport{
		Transport:    t,
		WriteChannel: make(chan WriteRequest, batchSize),
		CloseChannel: make(chan struct{}),
	}

	go bt.WriteBatchLoop()

	return bt
}

func (bt *BatchTransport) WriteBatchLoop() {
	for {
		select {
		case <-bt.CloseChannel:
			return
		case req := <-bt.WriteChannel:
			batch := make([][]byte, 1, 128)
			batch[0] = req.data

			pending := len(bt.WriteChannel)
			for i := 0; i < pending; i++ {
				select {
				case <-bt.CloseChannel:
					return
				case nextReq := <-bt.WriteChannel:
					batch = append(batch, nextReq.data)
				}
			}

			bt.WriteMutex.Lock()
			for _, d := range batch {
				if err := bt.Transport.WriteMessage(context.Background(), d); err != nil {
					bt.WriteMutex.Unlock()
					return
				}
			}
			bt.WriteMutex.Unlock()
		}
	}
}

func (bt *BatchTransport) ReadMessage(ctx context.Context) ([]byte, error) {
	return bt.Transport.ReadMessage(ctx)
}

func (bt *BatchTransport) WriteMessage(ctx context.Context, data []byte) error {
	select {
	case <-bt.CloseChannel:
		return ePixieClosed
	case bt.WriteChannel <- WriteRequest{data: data, ctx: ctx}:
		return nil
	default:
		bt.WriteMutex.Lock()
		err := bt.Transport.WriteMessage(ctx, data)
		bt.WriteMutex.Unlock()
		return err
	}
}

func (bt *BatchTransport) Close() error {
	bt.CloseOnce.Do(func() {
		close(bt.CloseChannel)
	})
	return bt.Transport.Close()
}
