package backends

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fake QueueBackend records sends and returns a queued reply.
type fakeQB struct {
	lastSend    SendOptions
	lastReceive ReceiveOptions
	sendErr     error
	reply       *Message
	receiveErr  error
}

func (f *fakeQB) Send(_ context.Context, opts SendOptions) error {
	f.lastSend = opts
	return f.sendErr
}

func (f *fakeQB) Receive(_ context.Context, opts ReceiveOptions) (*Message, error) {
	f.lastReceive = opts
	if f.receiveErr != nil {
		return nil, f.receiveErr
	}
	return f.reply, nil
}

func (f *fakeQB) Close() error { return nil }

// fake backend that implements the native capability.
type fakeRR struct {
	fakeQB
	called  bool
	gotOpts RequestOptions
	rrReply *Message
}

func (f *fakeRR) Request(_ context.Context, opts RequestOptions) (*Message, error) {
	f.called = true
	f.gotOpts = opts
	return f.rrReply, nil
}

func TestEnsureCorrelationIDPrecedence(t *testing.T) {
	// explicit wins
	o := RequestOptions{CorrelationID: "explicit", MessageID: "msg"}
	if got := EnsureCorrelationID(&o); got != "explicit" {
		t.Errorf("explicit correlation id should win, got %q", got)
	}
	// message id is reused when no correlation id
	o = RequestOptions{MessageID: "msg-id"}
	if got := EnsureCorrelationID(&o); got != "msg-id" {
		t.Errorf("expected correlation id derived from message id, got %q", got)
	}
	// otherwise generated, non-empty and prefixed
	o = RequestOptions{}
	got := EnsureCorrelationID(&o)
	if got == "" || !strings.HasPrefix(got, "xmc-") {
		t.Errorf("expected a generated xmc- correlation id, got %q", got)
	}
	if o.CorrelationID != got {
		t.Errorf("EnsureCorrelationID should mutate opts, got %q want %q", o.CorrelationID, got)
	}
	// generated ids are unique
	o2 := RequestOptions{}
	if EnsureCorrelationID(&o2) == got {
		t.Error("generated correlation ids should be unique")
	}
}

func TestRequestDefaultExchange(t *testing.T) {
	f := &fakeQB{reply: &Message{Data: []byte("pong")}}
	msg, err := Request(context.Background(), f, RequestOptions{Address: "A.foo", Message: []byte("ping")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(msg.Data) != "pong" {
		t.Errorf("reply = %q, want pong", msg.Data)
	}
	// auto-generated correlation id must be on the wire
	if f.lastSend.CorrelationID == "" {
		t.Error("expected an auto-generated correlation id on the sent request")
	}
	// default reply destination used for send + receive
	if f.lastSend.ReplyTo != DefaultReplyQueue {
		t.Errorf("send replyTo = %q, want %q", f.lastSend.ReplyTo, DefaultReplyQueue)
	}
	if f.lastReceive.Queue != DefaultReplyQueue || !f.lastReceive.Acknowledge {
		t.Errorf("receive opts = %+v, want queue=%q acknowledge=true", f.lastReceive, DefaultReplyQueue)
	}
}

func TestRequestAcceptsReplyWithoutCorrelationID(t *testing.T) {
	f := &fakeQB{reply: &Message{Data: []byte("ok")}} // no CorrelationID echoed
	if _, err := Request(context.Background(), f, RequestOptions{Address: "q", Message: []byte("x")}); err != nil {
		t.Fatalf("a reply without a correlation id should be accepted, got %v", err)
	}
}

func TestRequestDetectsMismatchedReply(t *testing.T) {
	f := &fakeQB{reply: &Message{Data: []byte("not-yours"), CorrelationID: "someone-else"}}
	_, err := Request(context.Background(), f, RequestOptions{Address: "q", Message: []byte("x"), CorrelationID: "mine"})
	if !errors.Is(err, ErrReplyMismatch) {
		t.Fatalf("expected ErrReplyMismatch, got %v", err)
	}
}

func TestRequestMatchingReplyAccepted(t *testing.T) {
	f := &fakeQB{reply: &Message{Data: []byte("yours"), CorrelationID: "mine"}}
	msg, err := Request(context.Background(), f, RequestOptions{Address: "q", Message: []byte("x"), CorrelationID: "mine"})
	if err != nil {
		t.Fatalf("matching reply should be accepted, got %v", err)
	}
	if string(msg.Data) != "yours" {
		t.Errorf("reply = %q, want yours", msg.Data)
	}
}

func TestRequestNoReply(t *testing.T) {
	f := &fakeQB{receiveErr: ErrNoMessageAvailable}
	_, err := Request(context.Background(), f, RequestOptions{Address: "q", Message: []byte("x")})
	if !errors.Is(err, ErrNoMessageAvailable) {
		t.Fatalf("expected ErrNoMessageAvailable, got %v", err)
	}
}

func TestRequestSendFailureReturnsTypedError(t *testing.T) {
	sendCause := errors.New("broker down")
	f := &fakeQB{sendErr: sendCause}
	_, err := Request(context.Background(), f, RequestOptions{Address: "q", Message: []byte("x")})
	var sendErr *RequestSendError
	if !errors.As(err, &sendErr) {
		t.Fatalf("expected *RequestSendError, got %T: %v", err, err)
	}
	if !errors.Is(sendErr.Err, sendCause) {
		t.Errorf("sendErr.Err = %v, want %v", sendErr.Err, sendCause)
	}
}

func TestRequestDispatchesToNativeCapability(t *testing.T) {
	f := &fakeRR{rrReply: &Message{Data: []byte("native")}}
	msg, err := Request(context.Background(), f, RequestOptions{Address: "q", Message: []byte("x")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !f.called {
		t.Error("expected native RequestReplyBackend.Request to be used")
	}
	if string(msg.Data) != "native" {
		t.Errorf("reply = %q, want native", msg.Data)
	}
	// the default send path must NOT have run on a native backend
	if f.fakeQB.lastSend.Queue != "" {
		t.Error("native backend should bypass the default send/receive path")
	}
}
