package llm

import (
	"errors"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
)

type temporaryNetworkError struct{}

func (temporaryNetworkError) Error() string   { return "temporary network error" }
func (temporaryNetworkError) Timeout() bool   { return false }
func (temporaryNetworkError) Temporary() bool { return true }

func TestIsTransientTransportError(t *testing.T) {
	for _, err := range []error{
		io.ErrUnexpectedEOF,
		syscall.ECONNREFUSED,
		syscall.ECONNRESET,
		syscall.EPIPE,
		syscall.ETIMEDOUT,
		&net.DNSError{IsTemporary: true},
		&url.Error{Op: "Post", URL: "https://api.example.test", Err: syscall.ECONNRESET},
		temporaryNetworkError{},
	} {
		if !IsTransientTransportError(err) {
			t.Fatalf("IsTransientTransportError(%T) = false, want true", err)
		}
	}
}

func TestIsTransientTransportErrorRejectsTerminalErrors(t *testing.T) {
	for _, err := range []error{
		nil,
		errors.New("invalid api key"),
		&net.DNSError{IsNotFound: true},
	} {
		if IsTransientTransportError(err) {
			t.Fatalf("IsTransientTransportError(%T) = true, want false", err)
		}
	}
}
