package channel

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyHandlerPreservesOrdinaryHTTPAndFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := proxyHandler{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte("stream event"))
		writer.(http.Flusher).Flush()
		if wrapped := writer.(*upgradeResponseWriter).Unwrap(); wrapped != recorder {
			t.Errorf("Unwrap returned %T, want the original response writer", wrapped)
		}
	})}
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://api.example/stream", nil))
	if recorder.Code != http.StatusAccepted || recorder.Body.String() != "stream event" || !recorder.Flushed {
		t.Errorf("response = %+v, want the unchanged streaming response", recorder)
	}
}

func TestProxyHandlerLeavesConnectWorkerOwnershipUnchanged(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := proxyHandler{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if writer != recorder {
			t.Errorf("CONNECT writer was wrapped: %T", writer)
		}
	})}
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodConnect, "http://api.example:443", nil))
}

func TestUpgradeResponseWriterPropagatesAnUnsupportedHijack(t *testing.T) {
	writer := &upgradeResponseWriter{ResponseWriter: httptest.NewRecorder()}
	connection, buffered, err := writer.Hijack()
	if connection != nil || buffered != nil || !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("Hijack = %v, %v, %v, want the underlying unsupported error", connection, buffered, err)
	}
	writer.closeUpgrade()
}
