package videocaller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestMaximumConnections tries to connect multiple users to websockets
// more than the maximum should be rejected
func TestMaximumConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	var ws1, ws2, ws3 *websocket.Conn

	defer func() {
		t.Log("Made it to defer")
		server.CloseClientConnections()
		server.Close()

		// cannot defer during the test, must do Closes on teardown
		if ws1 != nil {
			ws1.Close()
		}
		if ws2 != nil {
			ws2.Close()
		}
		if ws3 != nil {
			ws3.Close()
		}
	}()

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	if wsCount != 0 {
		t.Fatalf("unexpected active connections, expect 0; have: %d", wsCount)
		return
	}

	// first connection
	ws1, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
		return
	}

	time.Sleep(500 * time.Millisecond)

	defer ws1.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	time.Sleep(1 * time.Second)

	if wsCount != 1 {
		t.Fatalf("unexpected active connections, expect 1; have: %d", wsCount)
		return
	}

	msgType, msgBody, err := ws1.ReadMessage()
	if err != nil {
		t.Fatal(err)
		return
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("Unknown response message type: %d", msgType)
		return
	}

	msg1 := wsMsg{}
	if err = json.Unmarshal(msgBody, &msg1); err != nil {
		t.Fatal(err)
		return
	}

	if msg1.MsgType != wsMsgInitCaller {
		t.Fatalf("Unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
		return
	}

	// second connection
	ws2, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
		return
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected second response status: %d; expected %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	time.Sleep(1 * time.Second)

	if wsCount != 2 {
		t.Fatalf("unexpected active connections, expect 2; have: %d", wsCount)
		return
	}

	// Third person should not be able to connect to peer to peer call
	ws3, resp, err = websocket.DefaultDialer.Dial(wsUrl, nil)
	if err == nil {
		t.Fatal("expected error in third response")
		return
	}

	if resp.StatusCode != http.StatusLocked {
		t.Fatalf("unexpected third response status: %d; expected %d", resp.StatusCode, http.StatusLocked)
	}

	time.Sleep(1 * time.Second)

	if wsCount != 2 {
		t.Fatalf("unexpected active connections, expect 2; have: %d", wsCount)
		return
	}
}

// TestReconnectCaller test that if Caller drops connection,
// a new connection is reinitialized
func TestReconnectCaller(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	var ws1 *websocket.Conn

	defer func() {
		t.Log("Made it to defer")
		server.CloseClientConnections()
		server.Close()

		// cannot defer during the test, must do Closes on teardown
		if ws1 != nil {
			ws1.Close()
		}
	}()

	wsUrl := strings.Replace(server.URL, "http", "ws", 1)

	if wsCount != 0 {
		t.Fatalf("unexpected active connections, expect 0; have: %d", wsCount)
		return
	}

	// first connection
	ws1, resp, err := websocket.DefaultDialer.Dial(wsUrl, nil)
	if err != nil {
		t.Fatal(err)
		return
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected first response status: %d; expected: %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}

	msgType, msgBody, err := ws1.ReadMessage()
	if err != nil {
		t.Fatal(err)
		return
	}

	time.Sleep(1 * time.Second)

	if wsCount != 1 {
		t.Fatalf("unexpected number of active connections, expect 1; have: %d", wsCount)
		return
	}

	if msgType != websocket.TextMessage {
		t.Fatalf("Unknown response message type: %d", msgType)
		return
	}

	msg1 := wsMsg{}
	if err = json.Unmarshal(msgBody, &msg1); err != nil {
		t.Fatal(err)
		return
	}

	if msg1.MsgType != wsMsgInitCaller {
		t.Fatalf("Unknown response message body: %s; expected: %s", msg1.MsgType, wsMsgInitCaller)
		return
	}

	return
}

// // TestReconnectCaller test that if Callee drops connection,
// // a new connection is reinitialized
// func TestReconnectCallee(t *testing.T) {
// 	return
// }
