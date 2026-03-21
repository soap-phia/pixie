package pixie

import (
	"context"
	"runtime"
	"sync/atomic"
)

type FlowControlMode uint8

type FlowControl struct {
	Mode         FlowControlMode
	Buffer       atomic.Uint32
	TargetBuffer uint32
	Wakeup       chan struct{}
}

const (
	fcDisabled FlowControlMode = iota
	fcTrackAmt
	fcSendMessages
)

func NewFlowControl(mode FlowControlMode, bufferSize uint32) *FlowControl {
	fc := &FlowControl{
		Mode:         mode,
		TargetBuffer: (bufferSize * 9) / 10,
		Wakeup:       make(chan struct{}, 1),
	}
	fc.Buffer.Store(bufferSize)
	return fc
}

func (fc *FlowControl) CanSend() bool {
	if fc.Mode != fcTrackAmt {
		return true
	}
	return fc.Buffer.Load() > 0
}

func (fc *FlowControl) Subtract() {
	if fc.Mode == fcDisabled {
		return
	}

	for {
		old := fc.Buffer.Load()
		if old == 0 {
			return
		}

		new := old - 1
		if fc.Buffer.CompareAndSwap(old, new) {
			return
		}

		runtime.Gosched()
	}
}

func (fc *FlowControl) Add(amount uint32) uint32 {
	return fc.Buffer.Add(amount)
}

func (fc *FlowControl) Set(amount uint32) {
	fc.Buffer.Store(amount)
	select {
	case fc.Wakeup <- struct{}{}:
	default:
	}
}

func (fc *FlowControl) Get() uint32 {
	return fc.Buffer.Load()
}

func (fc *FlowControl) ShouldContinue(readCount uint32) bool {
	if fc.Mode != fcSendMessages {
		return false
	}
	return readCount >= fc.TargetBuffer
}

func (fc *FlowControl) WaitBuffer(ctx context.Context) error {
	if fc.CanSend() {
		return nil
	}

	select {
	case <-fc.Wakeup:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (fc *FlowControl) Awaken() {
	select {
	case fc.Wakeup <- struct{}{}:
	default:
	}
}
