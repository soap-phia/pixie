package pixie

import (
	"context"
	"sync"
)

type FlowControlMode uint8

type FlowControl struct {
	Mode         FlowControlMode
	Buffer       uint32
	TargetBuffer uint32
	Wakeup       chan struct{}
	Mutex        sync.Mutex
}

const (
	fcDisabled FlowControlMode = iota
	fcTrackAmt
	fcSendMessages
)

func NewFlowControl(mode FlowControlMode, bufferSize uint32) *FlowControl {
	return &FlowControl{
		Mode:         mode,
		Buffer:       bufferSize,
		TargetBuffer: (bufferSize * 9) / 10,
		Wakeup:       make(chan struct{}, 1),
	}
}

func (fc *FlowControl) CanSend() bool {
	if fc.Mode != fcTrackAmt {
		return true
	}
	fc.Mutex.Lock()
	defer fc.Mutex.Unlock()
	return fc.Buffer > 0
}

func (fc *FlowControl) Subtract() {
	if fc.Mode == fcDisabled {
		return
	}
	fc.Mutex.Lock()
	defer fc.Mutex.Unlock()
	if fc.Buffer > 0 {
		fc.Buffer--
	}
}

func (fc *FlowControl) Add(amount uint32) uint32 {
	fc.Mutex.Lock()
	defer fc.Mutex.Unlock()
	fc.Buffer += amount
	return fc.Buffer
}

func (fc *FlowControl) Set(amount uint32) {
	fc.Mutex.Lock()
	defer fc.Mutex.Unlock()
	fc.Buffer = amount
	select {
	case fc.Wakeup <- struct{}{}:
	default:
	}
}

func (fc *FlowControl) Get() uint32 {
	fc.Mutex.Lock()
	defer fc.Mutex.Unlock()
	return fc.Buffer
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
