package websocket

import (
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
)

var stripWebsocketHeaders = []string{
	"Upgrade",
	"Connection",
	"Sec-Websocket-Key",
	"Sec-Websocket-Version",
	"Sec-Websocket-Extensions",
}

// IsWebSocketUpgrade checks to see if the request is a WebSocket connection.
func IsWebSocketUpgrade(req *http.Request) bool {
	return websocket.IsWebSocketUpgrade(req)
}

// ClientConnect creates a WebSocket client connection for provided request. Caller is responsible for closing
// the connection. The response body may not contain the entire response and does
// not need to be closed by the application.
func ClientConnect(req *http.Request, dialler *websocket.Dialer) (*websocket.Conn, *http.Response, error) {
	req.URL.Scheme = ChangeRequestScheme(req.URL)
	wsHeaders := websocketHeaders(req)
	if dialler == nil {
		dialler = &websocket.Dialer{
			Proxy: http.ProxyFromEnvironment,
		}
	}
	conn, response, err := dialler.Dial(req.URL.String(), wsHeaders)
	if err != nil {
		return nil, response, err
	}
	response.Header.Set("Sec-WebSocket-Accept", generateAcceptKey(req))
	return conn, response, nil
}

// NewResponseHeader returns headers needed to return to origin for completing handshake
func NewResponseHeader(req *http.Request) http.Header {
	header := http.Header{}
	header.Add("Connection", "Upgrade")
	header.Add("Sec-Websocket-Accept", generateAcceptKey(req))
	header.Add("Upgrade", "websocket")
	return header
}

// the gorilla websocket library sets its own Upgrade, Connection, Sec-WebSocket-Key,
// Sec-WebSocket-Version and Sec-Websocket-Extensions headers.
// https://github.com/gorilla/websocket/blob/master/client.go#L189-L194.
func websocketHeaders(req *http.Request) http.Header {
	wsHeaders := make(http.Header)
	for key, val := range req.Header {
		wsHeaders[key] = val
	}
	// Assume the header keys are in canonical format.
	for _, header := range stripWebsocketHeaders {
		wsHeaders.Del(header)
	}
	wsHeaders.Set("Host", req.Host) // See TUN-1097
	return wsHeaders
}

// sha1Base64 sha1 and then base64 encodes str.
func sha1Base64(str string) string {
	hasher := sha1.New()
	_, _ = io.WriteString(hasher, str)
	hash := hasher.Sum(nil)
	return base64.StdEncoding.EncodeToString(hash)
}

// generateAcceptKey returns the string needed for the Sec-WebSocket-Accept header.
// https://tools.ietf.org/html/rfc6455#section-1.3 describes this process in more detail.
func generateAcceptKey(req *http.Request) string {
	return sha1Base64(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
}

// ChangeRequestScheme is needed as the gorilla websocket library requires the ws scheme.
// (even though it changes it back to http/https, but ¯\_(ツ)_/¯.)
func ChangeRequestScheme(reqURL *url.URL) string {
	switch reqURL.Scheme {
	case "https":
		return "wss"
	case "http":
		return "ws"
	case "":
		return "ws"
	default:
		return reqURL.Scheme
	}
}

// Stream copies copy data to & from provided io.ReadWriters.
func Stream(conn, backendConn io.ReadWriter, log *zerolog.Logger) {
	proxyDone := make(chan struct{}, 2)

	go func() {
		_, err := io.Copy(conn, backendConn)
		if err != nil {
			log.Debug().Msgf("conn to backendConn copy: %v", err)
		}
		proxyDone <- struct{}{}
	}()

	go func() {
		_, err := io.Copy(backendConn, conn)
		if err != nil {
			log.Debug().Msgf("backendConn to conn copy: %v", err)
		}
		proxyDone <- struct{}{}
	}()

	// If one side is done, we are done.
	<-proxyDone
}
