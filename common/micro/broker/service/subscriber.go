package service

import (
	"github.com/micro/go-micro/broker"

	pb "github.com/pydio/cells/common/proto/broker"
)

type serviceSub struct {
	topic   string
	queue   string
	handler broker.Handler
	stream  pb.Broker_SubscribeClient
	closed  chan bool
	options broker.SubscribeOptions
}

type serviceEvent struct {
	topic   string
	err     error
	message *broker.Message
}

func (s *serviceEvent) Topic() string {
	return s.topic
}

func (s *serviceEvent) Message() *broker.Message {
	return s.message
}

func (s *serviceEvent) Ack() error {
	return nil
}

func (s *serviceEvent) Error() error {
	return s.err
}

func (s *serviceSub) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *serviceSub) run() error {
	exit := make(chan bool)
	go func() {
		select {
		case <-exit:
		case <-s.closed:
		}

		// close the stream
		s.stream.Close()
	}()

	for {
		// TODO: do not fail silently
		msg, err := s.stream.Recv()
		if err != nil {
			// close the exit channel
			close(exit)

			// don't return an error if we unsubscribed
			if s.isClosed() {
				return nil
			}

			// return stream error
			return err
		}

		m := &broker.Message{
			Header: msg.Header,
			Body:   msg.Body,
		}

		e := &serviceEvent{
			topic:   s.topic,
			message: m,
		}

		// TODO: exec the subscriber error handler
		// in the event of an error
		s.handler(e)
	}
}

func (s *serviceSub) Options() broker.SubscribeOptions {
	return s.options
}

func (s *serviceSub) Topic() string {
	return s.topic
}

func (s *serviceSub) Unsubscribe() error {
	select {
	case <-s.closed:
		return nil
	default:
		close(s.closed)
	}
	return nil
}
